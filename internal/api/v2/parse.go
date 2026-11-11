package v2

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/gin-gonic/gin"
	"github.com/ledongthuc/pdf"
)

const maxParseFile = 50 << 20

func (h *Handler) HandleParse(c *gin.Context) {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		writeBadRequest(c, "Missing file upload. Send multipart/form-data with a 'file' field and optional 'options' JSON string.")
		return
	}
	if err := c.Request.ParseMultipartForm(maxParseFile); err != nil {
		writeBadRequest(c, "Uploaded file exceeds maximum size of 50MB.")
		return
	}
	if vals := c.Request.MultipartForm.Value["url"]; len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
		writeBadRequest(c, "url is not accepted for parse requests")
		return
	}
	if optStr := c.PostForm("options"); strings.TrimSpace(optStr) != "" {
		var opts struct {
			Formats         Formats         `json:"formats,omitempty"`
			OnlyMainContent *bool           `json:"onlyMainContent,omitempty"`
			IncludeTags     []string        `json:"includeTags,omitempty"`
			ExcludeTags     []string        `json:"excludeTags,omitempty"`
			Timeout         int             `json:"timeout,omitempty"`
			WaitFor         int             `json:"waitFor,omitempty"`
			Headers         json.RawMessage `json:"headers,omitempty"`
			Location        json.RawMessage `json:"location,omitempty"`
			MaxAge          int             `json:"maxAge,omitempty"`
			Origin          string          `json:"origin,omitempty"`
			Integration     *string         `json:"integration,omitempty"`
			Actions         []Action        `json:"actions,omitempty"`
			Screenshot      json.RawMessage `json:"screenshot,omitempty"`
			Branding        json.RawMessage `json:"branding,omitempty"`
			ChangeTracking  json.RawMessage `json:"changeTracking,omitempty"`
			Mobile          json.RawMessage `json:"mobile,omitempty"`
			Proxy           json.RawMessage `json:"proxy,omitempty"`
		}
		if err := strictUnmarshal([]byte(optStr), &opts); err != nil {
			writeBadRequest(c, "options must be a JSON object with supported parse fields")
			return
		}
		if len(opts.Actions) > 0 || len(opts.Screenshot) > 0 || len(opts.Branding) > 0 || len(opts.ChangeTracking) > 0 || opts.WaitFor > 0 || len(opts.Location) > 0 || len(opts.Mobile) > 0 {
			writeError(c, http.StatusBadRequest, CodeUnsupportedOptions, "parse does not support actions, screenshot, branding, changeTracking, waitFor > 0, location, or mobile")
			return
		}
		if len(opts.Proxy) > 0 {
			var s string
			if err := json.Unmarshal(opts.Proxy, &s); err != nil {
				writeError(c, http.StatusBadRequest, CodeUnsupportedOptions, "parse proxy must be auto or basic")
				return
			}
			if s != "auto" && s != "basic" {
				writeError(c, http.StatusBadRequest, CodeUnsupportedOptions, "parse proxy must be auto or basic")
				return
			}
		}
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		writeBadRequest(c, "Missing file upload. Send multipart/form-data with a 'file' field and optional 'options' JSON string.")
		return
	}
	defer file.Close()
	data := make([]byte, 0, header.Size)
	buf := make([]byte, 32*1024)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > maxParseFile {
				writeBadRequest(c, "Uploaded file exceeds maximum size of 50MB.")
				return
			}
		}
		if err != nil {
			break
		}
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	var markdown string
	switch ext {
	case ".txt", ".md":
		markdown = string(data)
	case ".html", ".htm":
		markdown, err = scraper.MarkdownFromHTML(data)
		if err != nil {
			writeBadRequest(c, "failed to parse HTML")
			return
		}
	case ".pdf":
		markdown, err = pdfText(data)
		if err != nil {
			msg := "Malformed PDF file."
			if strings.Contains(strings.ToLower(err.Error()), "encrypt") || strings.Contains(strings.ToLower(err.Error()), "password") {
				msg = "Encrypted PDFs are not supported."
			}
			writeBadRequest(c, msg)
			return
		}
	case ".docx":
		markdown, err = docxMarkdown(data)
		if err != nil {
			writeBadRequest(c, "Malformed DOCX file.")
			return
		}
	case ".doc":
		writeError(c, http.StatusBadRequest, CodeUnsupportedFile, "Unsupported upload type. Supported file extensions: .pdf, .docx, .html, .htm, .md, .txt")
		return
	default:
		writeError(c, http.StatusBadRequest, CodeUnsupportedFile, "Unsupported upload type. Supported file extensions: .pdf, .docx, .html, .htm, .md, .txt")
		return
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = "text/plain"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": Document{
		Markdown: markdown,
		Metadata: Metadata{URL: header.Filename, StatusCode: 200, ContentType: contentType},
	}})
}

func pdfText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	reader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	text, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(text)), nil
}

func docxMarkdown(data []byte) (string, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var document *zip.File
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			document = f
			break
		}
	}
	if document == nil {
		return "", fmt.Errorf("word/document.xml not found")
	}
	rc, err := document.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var paragraphs []string
	var current strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" {
				inText = true
			}
			if t.Name.Local == "p" {
				current.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
			if t.Name.Local == "p" {
				if text := strings.TrimSpace(current.String()); text != "" {
					paragraphs = append(paragraphs, text)
				}
			}
		case xml.CharData:
			if inText {
				current.Write([]byte(t))
			}
		}
	}
	return strings.Join(paragraphs, "\n\n"), nil
}
