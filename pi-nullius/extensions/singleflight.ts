/**
 * Single-flight wrapper: while a run is in flight, every caller joins it
 * instead of starting another. Used to serialize the hunt pipeline — the
 * hunt tool and the auto-hunt can both want it, and two concurrent runs
 * interleave at the awaits and corrupt the shared checklist. The flight
 * clears on settle (success or failure), so a failed run never pins the
 * wrapper shut.
 */
export function singleFlight<A extends unknown[], T>(
	fn: (...args: A) => Promise<T>,
): (...args: A) => Promise<T> {
	let inFlight: Promise<T> | null = null;
	return (...args: A) => {
		if (inFlight) return inFlight;
		inFlight = fn(...args).finally(() => {
			inFlight = null;
		});
		return inFlight;
	};
}
