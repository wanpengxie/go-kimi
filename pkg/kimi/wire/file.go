package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WireFileMetadata holds metadata for one wire JSONL file.
type WireFileMetadata struct {
	Path string `json:"path"`
}

// WireMessageRecord stores one timestamped wire envelope in JSONL.
type WireMessageRecord struct {
	Timestamp time.Time           `json:"timestamp"`
	Envelope  WireMessageEnvelope `json:"envelope"`
}

// Message deserializes the envelope into a concrete wire message.
func (r WireMessageRecord) Message() (WireMessage, error) {
	return DeserializeWireMessageEnvelope(r.Envelope)
}

// WireFile provides append and iteration operations over a JSONL wire log.
type WireFile struct {
	Path     string           `json:"path,omitempty"`
	Metadata WireFileMetadata `json:"metadata,omitempty"`
}

// NewWireFile creates a wire file descriptor bound to a JSONL path.
func NewWireFile(path string) *WireFile {
	return &WireFile{
		Path: path,
		Metadata: WireFileMetadata{
			Path: path,
		},
	}
}

// AppendMessage serializes and appends one wire message as a JSONL record.
func (f *WireFile) AppendMessage(msg WireMessage) error {
	if f == nil {
		return errors.New("wire file: nil")
	}
	path, err := f.filePath()
	if err != nil {
		return err
	}

	envelope, err := SerializeWireMessage(msg)
	if err != nil {
		return err
	}

	record := WireMessageRecord{
		Timestamp: time.Now().UTC(),
		Envelope:  envelope,
	}

	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("wire file: marshal record: %w", err)
	}

	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("wire file: mkdir %q: %w", dir, err)
		}
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("wire file: open %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("wire file: write %q: %w", path, err)
	}

	return nil
}

// WireFileIterator iterates one wire JSONL file record-by-record.
type WireFileIterator struct {
	file    *os.File
	scanner *bufio.Scanner
	current WireMessageRecord
	err     error
	lineNo  int
	closed  bool
}

// IterRecords creates an iterator over existing records.
func (f *WireFile) IterRecords() (*WireFileIterator, error) {
	if f == nil {
		return nil, errors.New("wire file: nil")
	}
	path, err := f.filePath()
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &WireFileIterator{scanner: newWireScanner(strings.NewReader(""))}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wire file: open %q: %w", path, err)
	}

	return &WireFileIterator{
		file:    file,
		scanner: newWireScanner(file),
	}, nil
}

// Next advances the iterator.
func (it *WireFileIterator) Next() bool {
	if it == nil || it.err != nil || it.scanner == nil {
		return false
	}

	for it.scanner.Scan() {
		it.lineNo++
		line := strings.TrimSpace(it.scanner.Text())
		if line == "" {
			continue
		}

		var record WireMessageRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			it.err = fmt.Errorf("wire file: parse line %d: %w", it.lineNo, err)
			return false
		}
		it.current = record
		return true
	}

	if err := it.scanner.Err(); err != nil {
		it.err = fmt.Errorf("wire file: scan: %w", err)
	}
	return false
}

// Record returns the current record at iterator position.
func (it *WireFileIterator) Record() WireMessageRecord {
	if it == nil {
		return WireMessageRecord{}
	}
	return it.current
}

// Err returns the terminal iterator error, if any.
func (it *WireFileIterator) Err() error {
	if it == nil {
		return nil
	}
	return it.err
}

// Close releases iterator resources.
func (it *WireFileIterator) Close() error {
	if it == nil || it.closed {
		return nil
	}
	it.closed = true
	if it.file != nil {
		if err := it.file.Close(); err != nil {
			return fmt.Errorf("wire file: close: %w", err)
		}
	}
	return nil
}

// IsEmpty reports whether the wire file contains at least one non-empty JSONL line.
func (f *WireFile) IsEmpty() bool {
	if f == nil {
		return true
	}
	path, err := f.filePath()
	if err != nil {
		return true
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := newWireScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			return false
		}
	}
	return scanner.Err() == nil
}

func (f *WireFile) filePath() (string, error) {
	if f == nil {
		return "", errors.New("wire file: nil")
	}
	if path := strings.TrimSpace(f.Path); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(f.Metadata.Path); path != "" {
		return path, nil
	}
	return "", errors.New("wire file: path is empty")
}

func newWireScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return scanner
}
