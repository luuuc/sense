package blast

import "github.com/luuuc/sense/internal/model"

// RetainedHolder is one interface-laundered may-retain row: a struct that
// holds the subject only behind an interface-typed field whose concrete
// satisfier is a carrier of the subject. Via is the interface the holder's
// field is typed as — when a holder reaches the subject through more than
// one interface, the lowest-ID interface is recorded so output is stable
// run to run.
//
// The claim is MAY-retain: an interface field can legally receive a
// non-carrier satisfier elsewhere, so these rows are surfaced as a weaker,
// separately-counted group and never feed TotalAffected.
type RetainedHolder struct {
	Symbol model.Symbol
	Via    model.Symbol
}
