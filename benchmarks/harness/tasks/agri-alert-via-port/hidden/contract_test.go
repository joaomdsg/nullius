package hidden

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Passwords/config are exported by score.sh; read them so tests stay in sync.
func managerPass() string { return envOr("AGRI_MANAGER_PASSWORD", "manager-pass") }
func viewerPass() string  { return envOr("AGRI_VIEWER_PASSWORD", "viewer-pass") }
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// postValues posts application/x-www-form-urlencoded, supporting repeated keys
// (needed for the sector_name/sector_label pairs).
func postValues(t *testing.T, c *http.Client, u string, v url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", u, strings.NewReader(v.Encode()))
	if err != nil {
		t.Fatalf("build POST %s: %v", u, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	return resp
}

func validContact(ref, name, phone string) url.Values {
	return url.Values{"reference": {ref}, "name": {name}, "phone_number": {phone}, "active": {"on"}}
}

// ---- auth ------------------------------------------------------------------

func TestT1_LoginManagerRedirectsHome(t *testing.T) {
	a := startApp(t, nil)
	c := newClient(t)
	resp := postForm(t, c, a.base+"/login", map[string]string{"password": managerPass()})
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("manager login Location=%q, want /", loc)
	}
	if ck := cookieFromResponse(resp, "session_token"); ck == nil {
		t.Fatal("login did not set session_token cookie")
	} else if !ck.HttpOnly {
		t.Fatal("session_token cookie must be HttpOnly")
	}
}

func TestT2_LoginViewerRedirectsMap(t *testing.T) {
	a := startApp(t, nil)
	c := newClient(t)
	resp := postForm(t, c, a.base+"/login", map[string]string{"password": viewerPass()})
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/map" {
		t.Fatalf("viewer login Location=%q, want /map", loc)
	}
}

func TestT3_LoginWrongPasswordRejected(t *testing.T) {
	a := startApp(t, nil)
	c := newClient(t)
	resp := postForm(t, c, a.base+"/login", map[string]string{"password": "definitely-wrong"})
	b := body(t, resp)
	mustStatus(t, resp.StatusCode, http.StatusUnauthorized)
	mustContain(t, b, "login:invalid", "wrong-password error token")
}

func TestT4_UnauthRedirectsToLogin(t *testing.T) {
	a := startApp(t, nil)
	c := newClient(t)
	resp := get(t, c, a.base+"/")
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("unauth GET / Location=%q, want /login", loc)
	}
}

func TestT5_WrongRoleForbidden(t *testing.T) {
	a := startApp(t, nil)
	viewer := login(t, a, viewerPass())
	for _, path := range []string{"/", "/admin/stations"} { // manager-only routes
		if st, _ := getBody(t, viewer, a.base+path); st != http.StatusForbidden {
			t.Fatalf("viewer GET %s status=%d, want 403", path, st)
		}
	}
	manager := login(t, a, managerPass())
	if st, _ := getBody(t, manager, a.base+"/map"); st != http.StatusForbidden {
		t.Fatalf("manager GET /map (viewer route) status=%d, want 403", st)
	}
}

// ---- contacts: listing + pagination ---------------------------------------

func TestT6_ContactsPagination(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	st, b := getBody(t, c, a.base+"/")
	mustStatus(t, st, http.StatusOK)
	mustContain(t, b, `data-count="20"`, "whole-list count attribute")
	if n := countSubstr(b, "data-contact-id="); n != 15 {
		t.Fatalf("page 1 rows=%d, want PAGE_SIZE 15", n)
	}
	_, b2 := getBody(t, c, a.base+"/?page=2")
	if n := countSubstr(b2, "data-contact-id="); n != 5 {
		t.Fatalf("page 2 rows=%d, want 5 (20 total)", n)
	}
	st3, b3 := getBody(t, c, a.base+"/?page=99")
	mustStatus(t, st3, http.StatusOK)
	if n := countSubstr(b3, "data-contact-id="); n != 0 {
		t.Fatalf("page 99 rows=%d, want 0 (past end, still 200)", n)
	}
}

func TestT7_ContactTitleHTMLEscaped(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	resp := postValues(t, c, a.base+"/contacts", validContact("C8888", "<b>x</b>", "911111111"))
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	// The created contact takes the next id and sorts last, so it lands on the
	// final page (PAGE_SIZE 15) — fetch that page, not page 1.
	b := lastListPage(t, c, a)
	mustContain(t, b, "&lt;b&gt;x&lt;/b&gt;", "escaped contact name")
	mustNotContain(t, b, "<b>x</b>", "raw unescaped name must not appear")
}

// ---- contacts: create + validation ----------------------------------------

func TestT8_CreateContactSucceeds(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	resp := postValues(t, c, a.base+"/contacts", validContact("C9001", "New Person", "912345678"))
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	_, b := getBody(t, c, a.base+"/")
	mustContain(t, b, `data-count="21"`, "count after create")
	// The new contact is id 21 (max was 20) and lands on page 2 (ids 16..21 = 6 rows);
	// tie the name to an actual row rather than accepting text anywhere on the page.
	_, p2 := getBody(t, c, a.base+"/?page=2")
	if n := countSubstr(p2, "data-contact-id="); n != 6 {
		t.Fatalf("page 2 rows after create=%d, want 6", n)
	}
	mustContain(t, p2, `data-contact-id="21"`, "new contact occupies id 21 row")
	mustContain(t, p2, "New Person", "new contact name in the list")
}

func TestT9_CreateNameRequired(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	resp := postValues(t, c, a.base+"/contacts", validContact("C9002", "   ", "912345678"))
	b := body(t, resp)
	mustStatus(t, resp.StatusCode, http.StatusBadRequest)
	mustContain(t, b, "error:name_required", "empty-name token")
	_, list := getBody(t, c, a.base+"/")
	mustContain(t, list, `data-count="20"`, "state unchanged on validation failure")
}

func TestT10_CreateReferenceRequired(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	v := validContact("", "Somebody", "912345678")
	resp := postValues(t, c, a.base+"/contacts", v)
	b := body(t, resp)
	mustStatus(t, resp.StatusCode, http.StatusBadRequest)
	mustContain(t, b, "error:reference_required", "empty-reference token")
}

func TestT11_CreatePhoneMustBeNineDigits(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	for _, bad := range []string{"12345", "1234567890", "91234567a"} {
		resp := postValues(t, c, a.base+"/contacts", validContact("C9003", "Phone Tester", bad))
		b := body(t, resp)
		mustStatus(t, resp.StatusCode, http.StatusBadRequest)
		mustContain(t, b, "error:phone_invalid", "bad phone "+bad)
	}
}

func TestT12_SectorValidation(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())

	// label > 25 runes -> error:label_toolong (step 4, before count/dup)
	long := validContact("C9101", "Label Long", "912345678")
	long.Add("sector_name", "S002")
	long.Add("sector_label", strings.Repeat("x", 26))
	rb := body(t, postValues(t, c, a.base+"/contacts", long))
	mustContain(t, rb, "error:label_toolong", "label > 25 runes")

	// > 3 sectors -> error:too_many_sectors (step 5)
	many := validContact("C9102", "Too Many", "912345678")
	for _, s := range []string{"S002", "S004", "S006", "S008"} {
		many.Add("sector_name", s)
		many.Add("sector_label", "ok")
	}
	mb := body(t, postValues(t, c, a.base+"/contacts", many))
	mustContain(t, mb, "error:too_many_sectors", "> 3 sectors")

	// duplicate sector name -> error:sector_duplicate (step 6)
	dup := validContact("C9103", "Dup Sector", "912345678")
	dup.Add("sector_name", "S002")
	dup.Add("sector_label", "a")
	dup.Add("sector_name", "S002")
	dup.Add("sector_label", "b")
	db := body(t, postValues(t, c, a.base+"/contacts", dup))
	mustContain(t, db, "error:sector_duplicate", "duplicate sector name")
}

func TestT13_EditContact(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	// valid edit of contact id 1
	ok := validContact("C1001", "Edited Name", "913333333")
	resp := patch(t, c, a.base+"/contacts/1", ok)
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	_, b := getBody(t, c, a.base+"/")
	mustContain(t, b, "Edited Name", "edited contact reflected")
	// bad id (non-integer AND <=0) -> 400 error:bad_id, status asserted
	for _, badID := range []string{"abc", "0"} {
		r := patch(t, c, a.base+"/contacts/"+badID, validContact("C1001", "x", "912345678"))
		bad := body(t, r)
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("PATCH /contacts/%s status=%d, want 400", badID, r.StatusCode)
		}
		mustContain(t, bad, "error:bad_id", "bad id "+badID)
	}
	// unknown (but valid) id -> 404
	un := patch(t, c, a.base+"/contacts/99999", validContact("C1001", "x", "912345678"))
	un.Body.Close()
	if un.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id PATCH status=%d, want 404", un.StatusCode)
	}
}

func TestT14_DeleteAndIdNotReused(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	// bad id -> 400 error:bad_id (non-integer)
	rbad := delReq(t, c, a.base+"/contacts/abc")
	bbad := body(t, rbad)
	if rbad.StatusCode != http.StatusBadRequest {
		t.Fatalf("DELETE /contacts/abc status=%d, want 400", rbad.StatusCode)
	}
	mustContain(t, bbad, "error:bad_id", "DELETE non-integer id")
	// delete contact id 20 (the current max)
	del := delReq(t, c, a.base+"/contacts/20")
	del.Body.Close()
	mustStatus(t, del.StatusCode, http.StatusSeeOther)
	_, b := getBody(t, c, a.base+"/?page=2")
	// only 4 left on page 2 (was 5), id 20 gone
	if n := countSubstr(b, "data-contact-id="); n != 4 {
		t.Fatalf("after delete, page 2 rows=%d, want 4", n)
	}
	mustNotContain(t, b, `data-contact-id="20"`, "deleted id gone")
	// create -> new id must be 21 (never reuse 20)
	resp := postValues(t, c, a.base+"/contacts", validContact("C9200", "Fresh", "914444444"))
	resp.Body.Close()
	_, b2 := getBody(t, c, a.base+"/?page=2")
	mustContain(t, b2, `data-contact-id="21"`, "new id is 21, not reused 20")
	mustNotContain(t, b2, `data-contact-id="20"`, "id 20 never reused")
}

// ---- stations --------------------------------------------------------------

func TestT15_StationsListAndJoin(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	st, b := getBody(t, c, a.base+"/admin/stations")
	mustStatus(t, st, http.StatusOK)
	mustContain(t, b, `data-count="30"`, "stations whole-list count")
	mustContain(t, b, "Field Site 01", "place joined from weather db")
	if n := countSubstr(b, "data-station-id="); n != 15 {
		t.Fatalf("stations page 1 rows=%d, want 15", n)
	}
	mustContain(t, b, `data-status=`, "status attribute present")
}

func TestT16_StationStatusChange(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	_, before := getBody(t, c, a.base+"/admin/stations?page=1")
	beforeInactive := countSubstr(before, `data-status="inactive"`)
	// ST001 seeds as active; change to inactive
	resp := postForm(t, c, a.base+"/admin/stations/ST001/status", map[string]string{"status": "inactive"})
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	_, after := getBody(t, c, a.base+"/admin/stations?page=1")
	afterInactive := countSubstr(after, `data-status="inactive"`)
	if afterInactive != beforeInactive+1 {
		t.Fatalf("inactive count %d -> %d, want +1 after status change", beforeInactive, afterInactive)
	}
}

// ---- grids -----------------------------------------------------------------

func TestT17_GridSectors(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, viewerPass())
	resp := get(t, c, a.base+"/grid/sectors")
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Fatalf("Content-Type=%q, want json", ct)
	}
	fc := parseGeoJSON(t, body(t, resp))
	if fc.Type != "FeatureCollection" {
		t.Fatalf("type=%q, want FeatureCollection", fc.Type)
	}
	if len(fc.Features) != 240 {
		t.Fatalf("sectors features=%d, want 240 (all cells)", len(fc.Features))
	}
	f := fc.Features[0]
	if f.Geometry["type"] != "Polygon" {
		t.Fatalf("geometry type=%v, want Polygon", f.Geometry["type"])
	}
	if _, ok := f.Properties["cell_id"]; !ok {
		t.Fatal("feature missing cell_id property")
	}
	if _, ok := f.Properties["sector_name"]; !ok {
		t.Fatal("feature missing sector_name property")
	}
}

func TestT18_GridSporulation(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, viewerPass())
	fc := parseGeoJSON(t, body(t, get(t, c, a.base+"/grid/sporulation")))
	if len(fc.Features) != 120 {
		t.Fatalf("sporulation features=%d, want 120 (monitored subset)", len(fc.Features))
	}
	// spot check: S002 -> max_status_id 2 (from seed feed)
	found := false
	for _, f := range fc.Features {
		if f.Properties["sector_name"] == "S002" {
			found = true
			if v, ok := f.Properties["max_status_id"].(float64); !ok || int(v) != 2 {
				t.Fatalf("S002 max_status_id=%v, want 2", f.Properties["max_status_id"])
			}
		}
	}
	if !found {
		t.Fatal("monitored sector S002 not in sporulation grid")
	}
}

func TestT19_GridThermalDay(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, viewerPass())
	fc := parseGeoJSON(t, body(t, get(t, c, a.base+"/grid/thermal?day=3")))
	if len(fc.Features) != 120 {
		t.Fatalf("thermal features=%d, want 120 (monitored subset)", len(fc.Features))
	}
	// S004 (monitored) has day3_status_id == 2 from the seed feed — a value check
	// (not just key presence) so an all-zero grid that skips the feed join fails.
	for _, f := range fc.Features {
		if f.Properties["sector_name"] == "S004" {
			v, ok := f.Properties["day3_status_id"].(float64)
			if !ok {
				t.Fatal("thermal feature missing numeric day3_status_id property")
			}
			if int(v) != 2 {
				t.Fatalf("S004 day3_status_id=%v, want 2 (from seed feed)", f.Properties["day3_status_id"])
			}
			return
		}
	}
	t.Fatal("monitored sector S004 not in thermal grid")
}

func TestT20_GridBattery(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, viewerPass())
	fc := parseGeoJSON(t, body(t, get(t, c, a.base+"/grid/battery")))
	if len(fc.Features) != 30 {
		t.Fatalf("battery features=%d, want 30 (one per station)", len(fc.Features))
	}
	f := fc.Features[0]
	if f.Geometry["type"] != "Point" {
		t.Fatalf("battery geometry=%v, want Point", f.Geometry["type"])
	}
	if coords, ok := f.Geometry["coordinates"].([]any); !ok || len(coords) != 2 {
		t.Fatalf("battery coordinates=%v, want [lon,lat]", f.Geometry["coordinates"])
	}
	for _, k := range []string{"station_id", "bat_volt", "timestamp"} {
		if _, ok := f.Properties[k]; !ok {
			t.Fatalf("battery feature missing %s property", k)
		}
	}
}

func TestT21_GridAuth(t *testing.T) {
	a := startApp(t, nil)
	// manager (wrong role for grids) -> 403 on every grid endpoint
	manager := login(t, a, managerPass())
	for _, path := range []string{"/grid/sectors", "/grid/sporulation", "/grid/thermal", "/grid/battery"} {
		if st, _ := getBody(t, manager, a.base+path); st != http.StatusForbidden {
			t.Fatalf("manager GET %s status=%d, want 403", path, st)
		}
	}
	// unauth -> redirect to login
	resp := get(t, newClient(t), a.base+"/grid/sectors")
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
}

// ---- SMS broadcast ---------------------------------------------------------

func TestT22_SMSBroadcast(t *testing.T) {
	// empty message -> 400 sms:empty
	a0 := startApp(t, map[string]string{"AGRI_SMS_URL": "http://127.0.0.1:9/none"})
	c0 := login(t, a0, managerPass())
	r0 := postForm(t, c0, a0.base+"/sms", map[string]string{"message": "   "})
	rb := body(t, r0)
	mustStatus(t, r0.StatusCode, http.StatusBadRequest)
	mustContain(t, rb, "sms:empty", "empty-message token")

	// success path: mock upstream returns 200
	okMock := newSMSMock(t, false)
	a1 := startApp(t, map[string]string{"AGRI_SMS_URL": okMock.srv.URL})
	c1 := login(t, a1, managerPass())
	resp := postForm(t, c1, a1.base+"/sms", map[string]string{"message": "frost warning"})
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusOK, http.StatusSeeOther)
	if okMock.count() != 1 {
		t.Fatalf("SMS upstream received %d messages, want 1", okMock.count())
	}

	// failure path: mock upstream returns 500 -> port responds 502
	failMock := newSMSMock(t, true)
	a2 := startApp(t, map[string]string{"AGRI_SMS_URL": failMock.srv.URL})
	c2 := login(t, a2, managerPass())
	resp2 := postForm(t, c2, a2.base+"/sms", map[string]string{"message": "will fail"})
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadGateway {
		t.Fatalf("SMS upstream failure -> status=%d, want 502", resp2.StatusCode)
	}
}

// ---- login page + logout ---------------------------------------------------

func TestT23_LoginPageForm(t *testing.T) {
	a := startApp(t, nil)
	st, b := getBody(t, newClient(t), a.base+"/login")
	mustStatus(t, st, http.StatusOK)
	mustContain(t, b, `action="/login"`, "login form posts to /login")
	mustContain(t, b, `name="password"`, "login form has a password input")
}

func TestT24_Logout(t *testing.T) {
	a := startApp(t, nil)
	c := login(t, a, managerPass())
	// sanity: authed
	if st, _ := getBody(t, c, a.base+"/"); st != http.StatusOK {
		t.Fatalf("pre-logout GET / status=%d, want 200", st)
	}
	resp := postForm(t, c, a.base+"/logout", map[string]string{})
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("logout Location=%q, want /login", loc)
	}
	// session cleared: protected GET now redirects to /login
	after := get(t, c, a.base+"/")
	after.Body.Close()
	mustStatus(t, after.StatusCode, http.StatusSeeOther)
	if loc := after.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("post-logout GET / Location=%q, want /login", loc)
	}
}

// ---- small helpers for PATCH/DELETE -----------------------------------------

func patch(t *testing.T, c *http.Client, u string, v url.Values) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PATCH", u, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", u, err)
	}
	return resp
}

func delReq(t *testing.T, c *http.Client, u string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("DELETE", u, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", u, err)
	}
	return resp
}
