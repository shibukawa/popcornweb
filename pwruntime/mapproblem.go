package pwruntime

import (
	"errors"
	"net/http"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

// MapProblem turns any error into the problem a response describes.
//
// Three cases, in order: an error that is already a Problem keeps everything it
// declared and gains the defaults it left out; an error the binding layer
// recognizes carries its own status, title and field failures across; anything
// else is an internal error, which is the only honest answer for a cause this
// package cannot classify.
//
// It is here rather than in either runtime because both answer the same
// failures, and a status that differed between two builds of one application
// would be a difference nothing in either response explained.
func MapProblem(err error) Problem {
	if err == nil {
		return InternalServerError(errors.New("nil error"))
	}
	var problem Problem
	if errors.As(err, &problem) {
		if problem.Status == 0 {
			problem.Status = http.StatusInternalServerError
		}
		if problem.Title == "" {
			problem.Title = http.StatusText(problem.Status)
		}
		return problem
	}
	if mapped, ok := tinybind.AsHTTPError(err); ok {
		message := mapped.Problem.Message
		if message == "" {
			message = mapped.Title
		}
		return Problem{
			Status: mapped.Status, Title: mapped.Title, Code: mapped.Problem.Code,
			Message: message, Fields: append([]FieldError(nil), mapped.Fields...), Cause: err,
		}
	}
	return InternalServerError(err)
}
