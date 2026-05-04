package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/textutil"
)

// CommandRunner runs an external converter and returns its standard output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Builtin parses common formats without external services.
type Builtin struct {
	// Run executes the legacy .doc converter; nil uses os/exec. LookPath
	// reports whether a converter exists; nil uses exec.LookPath.
	Run      CommandRunner
	LookPath func(string) (string, error)
}

// Name implements Engine.
func (b *Builtin) Name() string { return "builtin" }

// Probe is always ok: nothing to reach.
func (b *Builtin) Probe(context.Context) (bool, string) { return true, "built in" }

// zipReadLimit bounds any single archive member we inflate.
const zipReadLimit = 64 << 20

// Extract dispatches on extension and MIME.
func (b *Builtin) Extract(ctx context.Context, r Request) (*Result, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(r.Name), "."))
	mime := strings.ToLower(r.MIME)
	switch {
	case ext == "docx" || strings.Contains(mime, "wordprocessingml"):
		return b.docx(r)
	case ext == "xlsx" || strings.Contains(mime, "spreadsheetml"):
		return b.xlsx(r)
	case ext == "csv" || mime == "text/csv":
		return b.csv(r)
	case ext == "doc" || mime == "application/msword":
		return b.doc(ctx, r)
	case ext == "xls" || mime == "application/vnd.ms-excel":
		return nil, coded("builtin_unsupported", "legacy .xls files need an external extraction engine")
	case ext == "pdf" || mime == "application/pdf":
		return nil, coded("builtin_unsupported", "PDF files need an external extraction engine")
	case textutil.IsTextFile(r.Name, r.MIME):
		return b.text(r)
	}
	return nil, coded("builtin_unsupported", "no built-in parser for this file type")
}

func (b *Builtin) text(r Request) (*Result, error) {
	data := r.Data
	if !utf8.Valid(data) {
		data = bytes.ToValidUTF8(data, []byte("�"))
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, coded("builtin_empty_content", "")
	}
	return &Result{Text: text, Engine: "builtin"}, nil
}

func readZipMember(zr *zip.Reader, name string) ([]byte, bool) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, false
			}
			defer rc.Close()
			data, err := io.ReadAll(io.LimitReader(rc, zipReadLimit))
			return data, err == nil
		}
	}
	return nil, false
}

func openZip(data []byte) (*zip.Reader, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, coded("builtin_unprocessable", "not a valid office document")
	}
	return zr, nil
}

// docx reads word/document.xml: paragraphs become lines, table cells are
// separated by tabs and rows by newlines.
func (b *Builtin) docx(r Request) (*Result, error) {
	zr, err := openZip(r.Data)
	if err != nil {
		return nil, err
	}
	doc, ok := readZipMember(zr, "word/document.xml")
	if !ok {
		return nil, coded("builtin_unprocessable", "word/document.xml is missing")
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var out strings.Builder
	inText := false
	cellOpen := false
	cellDepth := 0
	pendingSep := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				out.WriteByte('\t')
			case "br", "cr":
				out.WriteByte('\n')
			case "tc":
				if cellOpen {
					out.WriteByte('\t')
				}
				cellOpen = true
				cellDepth++
				pendingSep = false
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if cellDepth == 0 {
					out.WriteByte('\n')
				} else {
					pendingSep = true
				}
			case "tc":
				cellDepth--
				pendingSep = false
			case "tr":
				cellOpen = false
				out.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				if pendingSep {
					out.WriteByte(' ')
					pendingSep = false
				}
				out.Write(t)
			}
		}
	}
	text := NormalizeText(out.String())
	if text == "" {
		return nil, coded("builtin_empty_content", "")
	}
	return &Result{Text: text, Engine: "builtin"}, nil
}

func colIndex(ref string) int {
	n := 0
	for _, c := range ref {
		if c >= 'A' && c <= 'Z' {
			n = n*26 + int(c-'A'+1)
		} else if c >= 'a' && c <= 'z' {
			n = n*26 + int(c-'a'+1)
		} else {
			break
		}
	}
	return n - 1
}

type xlsxSheet struct{ name, path string }

func (b *Builtin) xlsx(r Request) (*Result, error) {
	zr, err := openZip(r.Data)
	if err != nil {
		return nil, err
	}
	var shared []string
	if data, ok := readZipMember(zr, "xl/sharedStrings.xml"); ok {
		shared = parseSharedStrings(data)
	}
	sheets := xlsxSheets(zr)
	var out strings.Builder
	for _, sh := range sheets {
		data, ok := readZipMember(zr, sh.path)
		if !ok {
			continue
		}
		rows := parseSheetRows(data, shared)
		out.WriteString("Sheet: " + sh.name + "\n")
		for _, row := range rows {
			out.WriteString(strings.Join(row, " | ") + "\n")
		}
		out.WriteString("\n")
	}
	text := strings.TrimSpace(out.String())
	if strings.Count(text, "\n") == 0 && strings.HasPrefix(text, "Sheet:") || text == "" {
		return nil, coded("builtin_empty_content", "")
	}
	return &Result{Text: text, Engine: "builtin"}, nil
}

func xlsxSheets(zr *zip.Reader) []xlsxSheet {
	wb, ok := readZipMember(zr, "xl/workbook.xml")
	if !ok {
		return nil
	}
	rels := map[string]string{}
	if rb, ok := readZipMember(zr, "xl/_rels/workbook.xml.rels"); ok {
		var doc struct {
			Rel []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if xml.Unmarshal(rb, &doc) == nil {
			for _, r := range doc.Rel {
				t := strings.TrimPrefix(r.Target, "/")
				if !strings.HasPrefix(t, "xl/") {
					t = "xl/" + t
				}
				rels[r.ID] = t
			}
		}
	}
	var doc struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(wb, &doc) != nil {
		return nil
	}
	var out []xlsxSheet
	for i, s := range doc.Sheets {
		p := rels[s.ID]
		if p == "" {
			p = "xl/worksheets/sheet" + strconv.Itoa(i+1) + ".xml"
		}
		out = append(out, xlsxSheet{name: s.Name, path: p})
	}
	return out
}

func parseSharedStrings(data []byte) []string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []string
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = inSI
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
				inSI = false
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
	}
	return out
}

func parseSheetRows(data []byte, shared []string) [][]string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rows [][]string
	var row []string
	var cellType, cellRef string
	var val strings.Builder
	inV, inT, inCell := false, false, false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
			case "c":
				inCell = true
				cellType, cellRef = "", ""
				val.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						cellRef = a.Value
					}
				}
			case "v":
				inV = inCell
			case "t":
				inT = inCell
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				text := val.String()
				if cellType == "s" {
					if i, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && i >= 0 && i < len(shared) {
						text = shared[i]
					}
				}
				idx := colIndex(cellRef)
				for len(row) < idx {
					row = append(row, "")
				}
				row = append(row, strings.TrimSpace(text))
				inCell = false
			case "row":
				for len(row) > 0 && row[len(row)-1] == "" {
					row = row[:len(row)-1]
				}
				if len(row) > 0 {
					rows = append(rows, row)
				}
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
			}
		}
	}
	return rows
}

func (b *Builtin) csv(r Request) (*Result, error) {
	cr := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(r.Data, []byte("\xef\xbb\xbf"))))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	var out strings.Builder
	name := strings.TrimSuffix(filepath.Base(r.Name), filepath.Ext(r.Name))
	if name == "" || name == "." {
		name = "csv"
	}
	out.WriteString("Sheet: " + name + "\n")
	rows := 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if strings.Join(rec, "") == "" {
			continue
		}
		out.WriteString(strings.Join(rec, " | ") + "\n")
		rows++
	}
	if rows == 0 {
		return nil, coded("builtin_empty_content", "")
	}
	return &Result{Text: strings.TrimSpace(out.String()), Engine: "builtin"}, nil
}

// doc converts legacy Word files with antiword or, on macOS, textutil. The
// result names the engine that produced it.
func (b *Builtin) doc(ctx context.Context, r Request) (*Result, error) {
	look := b.LookPath
	if look == nil {
		look = exec.LookPath
	}
	run := b.Run
	if run == nil {
		run = execRunner
	}
	tmp, err := os.CreateTemp("", "riochat-doc-*.doc")
	if err != nil {
		return nil, coded("builtin_unprocessable", "cannot stage the file")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(r.Data); err != nil {
		tmp.Close()
		return nil, coded("builtin_unprocessable", "cannot stage the file")
	}
	tmp.Close()
	type conv struct {
		bin  string
		args []string
	}
	for _, c := range []conv{{"antiword", []string{tmp.Name()}}, {"textutil", []string{"-convert", "txt", "-stdout", tmp.Name()}}} {
		if _, err := look(c.bin); err != nil {
			continue
		}
		out, err := run(ctx, c.bin, c.args...)
		if err != nil {
			continue
		}
		if text := NormalizeText(string(out)); text != "" {
			return &Result{Text: text, Engine: c.bin}, nil
		}
	}
	return nil, coded("builtin_unsupported", "no converter for .doc files (install antiword)")
}

var _ = sort.Strings
