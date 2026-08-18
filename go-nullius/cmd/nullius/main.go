// Command nullius drives a mandate-driven build through the phase machine
// in internal/drive. All user interaction is editing mandate.md and
// rerunning `nullius drive` (DESIGN-mandates.md §9 v0 scope).
//
//	nullius init <slug> [-intent "..."] [-files a.go,b.go] [-headless] [-recon=scouts|ast|both] [-rule-patches]
//	nullius drive [<slug>] [-headless] [-recon=scouts|ast|both] [-rule-patches] [-adapter=api|claude|pi] [-craftsman-bin=...]
//	nullius status [<slug> ...]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"go-nullius/internal/caller"
	"go-nullius/internal/dispatch"
	"go-nullius/internal/drive"
	"go-nullius/internal/machine"
	"go-nullius/internal/mandate"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(os.Args[2:])
	case "drive":
		err = runDrive(os.Args[2:])
	case "status":
		err = runStatus(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nullius:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  nullius init <slug> [-intent "..."] [-files a.go,b.go] [-headless] [-recon=scouts|ast|both] [-rule-patches]
  nullius drive [<slug>] [-headless] [-recon=scouts|ast|both] [-rule-patches] [-adapter=api|claude|pi] [-craftsman-bin=...]
  nullius status [<slug> ...]`)
}

// liftSlug lets the slug precede the flags (`nullius init <slug> -intent
// ...`), matching the documented usage: Go's flag package stops parsing at
// the first positional, so flags after the slug would otherwise be
// silently swallowed as arguments.
func liftSlug(args []string) (slug string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return os.Getwd()
	}
	return strings.TrimSpace(string(out)), nil
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	intent := fs.String("intent", "", "the INTENT section text")
	files := fs.String("files", "", "comma-separated scope files")
	headless := fs.Bool("headless", false, "auto-fill ASSUMED recommendations, never block on interview cards")
	recon := fs.String("recon", "scouts", "recon arm: scouts|ast|both")
	rulePatches := fs.Bool("rule-patches", false, "RULE may emit unified diffs for trivial fixes, applied mechanically")
	slug, rest := liftSlug(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if slug == "" && fs.NArg() == 1 {
		slug = fs.Arg(0)
	} else if fs.NArg() > 0 {
		return fmt.Errorf("usage: nullius init <slug>")
	}
	if slug == "" {
		return fmt.Errorf("usage: nullius init <slug>")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	var fileList []string
	if *files != "" {
		for _, f := range strings.Split(*files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				fileList = append(fileList, f)
			}
		}
	}
	if _, err := mandate.Scaffold(root, slug, gitHead(root), mandate.InitOptions{
		Intent: *intent, Files: fileList, Headless: *headless, ReconMode: recon2mode(*recon), RulePatches: *rulePatches,
	}); err != nil {
		return err
	}
	fmt.Printf("nullius: scaffolded %s at %s\n", slug, mandate.Paths(root, slug).Dir)
	return nil
}

func recon2mode(s string) string {
	switch s {
	case "ast", "both":
		return s
	default:
		return "scouts"
	}
}

func runDrive(args []string) error {
	fs := flag.NewFlagSet("drive", flag.ExitOnError)
	headless := fs.Bool("headless", false, "override: auto-fill ASSUMED recommendations, never block")
	reconFlag := fs.String("recon", "", "override recon arm: scouts|ast|both")
	rulePatchesFlag := fs.Bool("rule-patches", false, "override: enable --rule-patches")
	adapterName := fs.String("adapter", "api", "dispatch adapter: api|claude|pi")
	craftsmanBin := fs.String("craftsman-bin", "", "path to a go-nullius binary used as EXECUTE's craftsman")
	slug, rest := liftSlug(args)
	if err := fs.Parse(rest); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	if slug == "" {
		slug = fs.Arg(0)
	}
	if slug == "" {
		slug, err = onlyMandateSlug(root)
		if err != nil {
			return err
		}
	}

	s, err := mandate.LoadState(root, slug)
	if err != nil {
		return err
	}
	if *headless {
		s.Headless = true
	}
	if *reconFlag != "" {
		s.ReconMode = recon2mode(*reconFlag)
	}
	if *rulePatchesFlag {
		s.RulePatches = true
	}
	if err := s.Save(root); err != nil {
		return err
	}

	adapter, err := buildAdapter(*adapterName)
	if err != nil {
		return err
	}

	var craftsman drive.Writer
	if *craftsmanBin != "" {
		craftsman = machine.SubprocessCraftsman{Bin: *craftsmanBin, Model: envOr("NULLIUS_FAST_MODEL", ""), Dir: root}
	}

	m := drive.New(drive.Config{Root: root, Slug: slug, Adapter: adapter, Craftsman: craftsman})
	result, driveErr := m.Drive(context.Background())
	if result != nil {
		fmt.Printf("nullius: %s phase=%s mode=%s\n", slug, result.Phase, result.Mode)
	}
	return driveErr
}

func buildAdapter(name string) (dispatch.Adapter, error) {
	if name != "api" {
		return dispatch.New(name, dispatch.Options{})
	}
	c := caller.New(os.Getenv("OPENAI_API_KEY"), map[caller.Tier]caller.Endpoint{
		caller.Fast:  {BaseURL: envOr("NULLIUS_FAST_URL", "http://localhost:8081/v1"), Model: envOr("NULLIUS_FAST_MODEL", "")},
		caller.Smart: {BaseURL: envOr("NULLIUS_SMART_URL", "http://localhost:8080/v1"), Model: envOr("NULLIUS_SMART_MODEL", "")},
	})
	return dispatch.New("api", dispatch.Options{Caller: c})
}

func runStatus(args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	slugs := args
	if len(slugs) == 0 {
		if slugs, err = allMandateSlugs(root); err != nil {
			return err
		}
	}
	for _, slug := range slugs {
		s, err := mandate.LoadState(root, slug)
		if err != nil {
			fmt.Printf("%s: %v\n", slug, err)
			continue
		}
		confirmed := 0
		for _, e := range s.Checklist {
			if e.Disposition == mandate.DispConfirmed {
				confirmed++
			}
		}
		fmt.Printf("%-24s phase=%-10s mode=%-8s confirmed=%d\n", slug, s.Phase, s.Mode, confirmed)
	}
	return nil
}

func onlyMandateSlug(root string) (string, error) {
	slugs, err := allMandateSlugs(root)
	if err != nil {
		return "", err
	}
	if len(slugs) != 1 {
		return "", fmt.Errorf("expected exactly one mandate under .nullius/mandates, found %d — name one explicitly", len(slugs))
	}
	return slugs[0], nil
}

func allMandateSlugs(root string) ([]string, error) {
	entries, err := os.ReadDir(root + "/.nullius/mandates")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var slugs []string
	for _, e := range entries {
		if e.IsDir() {
			slugs = append(slugs, e.Name())
		}
	}
	return slugs, nil
}
