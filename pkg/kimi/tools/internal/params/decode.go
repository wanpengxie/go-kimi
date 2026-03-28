package params

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// DecodeStrict decodes JSON params into dst with strict unknown-field checks.
// Empty and null payloads are treated as omitted params.
func DecodeStrict(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON values")
		}
		return err
	}

	return nil
}
