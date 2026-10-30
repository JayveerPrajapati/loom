package graph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Load reads and parses one workflow file.
func Load(path string) (*Workflow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	wf, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return wf, nil
}

// Parse decodes a workflow and rejects unknown fields so typos surface early.
func Parse(data []byte) (*Workflow, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var wf Workflow
	if err := dec.Decode(&wf); err != nil {
		return nil, err
	}

	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON")
		}
		return nil, err
	}

	return &wf, nil
}
