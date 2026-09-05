package cli

import "errors"

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// ExitCode keeps JSON stdout clean while exposing stable automation categories.
func ExitCode(err error) int {
	var api *APIError
	if errors.As(err, &api) {
		switch {
		case api.Status == 401 || api.Status == 403:
			return 3
		case api.Status == 409:
			return 4
		case api.Status == 0 || api.Status >= 500 || api.Status == 429:
			return 5
		default:
			return 2
		}
	}
	return 1
}
