package dataplane

import (
	"encoding/json"
	"io"
	"net/http"
)

func decodeJSON[T any](r *http.Request) (raw []byte, in T, err error) {
	raw, err = io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		return nil, in, &BadRequestError{}
	}
	if len(raw) > 0 {
		if unmarshalErr := json.Unmarshal(raw, &in); unmarshalErr != nil {
			return nil, in, &BadRequestError{}
		}
	}
	return raw, in, nil
}
