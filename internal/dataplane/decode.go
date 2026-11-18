package dataplane

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"altalune.id/openwa/internal/device"
)

func decodeJSON[T any](r *http.Request) (raw []byte, in T, err error) {
	raw, err = io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		return nil, in, &BadRequestError{}
	}
	if len(raw) > 0 {
		if unmarshalErr := json.Unmarshal(raw, &in); unmarshalErr != nil {
			if ir, ok := errors.AsType[*device.InvalidRulesError](unmarshalErr); ok {
				return nil, in, ir
			}
			return nil, in, &BadRequestError{}
		}
	}
	return raw, in, nil
}
