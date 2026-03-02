package llm

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MaxFrameBytes bounds one server-sent-event frame.
const MaxFrameBytes = 64 << 20

type sseFrame struct {
	Event string
	Data  string
	ID    string
}

// sseReader parses a text/event-stream body. Frames are separated by a blank
// line; "data:" lines of one frame are joined with newlines.
type sseReader struct {
	r   *bufio.Reader
	max int
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(r, 64<<10), max: MaxFrameBytes}
}

var errFrameTooLarge = errors.New("server-sent event frame exceeds the maximum size")

// readLine returns one line without its terminator.
func (s *sseReader) readLine(budget *int) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := s.r.ReadSlice('\n')
		*budget -= len(chunk)
		if *budget < 0 {
			return "", errFrameTooLarge
		}
		sb.Write(chunk)
		switch {
		case err == nil:
			return strings.TrimRight(sb.String(), "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			if sb.Len() > 0 && errors.Is(err, io.EOF) {
				return strings.TrimRight(sb.String(), "\r\n"), nil
			}
			return "", err
		}
	}
}

// next returns the next frame; io.EOF when the stream ends cleanly.
func (s *sseReader) next() (sseFrame, error) {
	var f sseFrame
	var data []string
	have := false
	budget := s.max
	for {
		line, err := s.readLine(&budget)
		if err != nil {
			if errors.Is(err, io.EOF) && have {
				f.Data = strings.Join(data, "\n")
				return f, nil
			}
			return sseFrame{}, err
		}
		if line == "" {
			if !have {
				continue
			}
			f.Data = strings.Join(data, "\n")
			return f, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "event":
			f.Event, have = value, true
		case "data":
			data, have = append(data, value), true
		case "id":
			f.ID, have = value, true
		}
	}
}

func (f sseFrame) String() string { return fmt.Sprintf("event=%q data=%d bytes", f.Event, len(f.Data)) }
