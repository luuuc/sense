package smoke

// reconcileLedger is unexported, has no caller, and its name is mentioned
// nowhere else — the genuinely-dead shape the Go voice earns `dead` for.
func reconcileLedger() {}

// init is run by the Go runtime at package load, not by a caller. The Go voice
// keeps it possibly_dead (go_init); it must never earn `dead`.
func init() {
	_ = 1
}
