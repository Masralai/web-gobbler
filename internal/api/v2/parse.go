package v2

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Masralai/web-gobbler/internal/scraper"
	"github.com/gin-gonic/gin"
	"github.com/ledongthuc/pdf"
)

const maxParseFile = 50 << 20

func (h *Handler) HandleParse(c *gin.Context) {
	if !strings.Contains(c.GetHeader("Content-Type"), "multipart/form-data") {
		writeBadRequest(c, "Missing file upload. Send multipart/form-data with a 'file' field and optional 'options' JSON string.")
		return
	}
	if err := c.Request.ParseMultipartForm(maxParseFile); err != nil {
		writeError(c, 400, CodeBadRequest, "Uploaded file exceeds maximum size of 50MB.")
		return
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
			writeBadRequest(c, "failed to parse PDF")
			return
		}
	case ".docx":
		markdown, err = docxMarkdown(data)
		if err != nil {
			writeBadRequest(c, "failed to parse DOCX")
			return
		}
	case ".doc":
		writeError(c, http.StatusBadRequest, CodeUnsupportedFile, "Unsupported upload type. Supported file extensions: .pdf, .docx, .html, .htm, .md, .txt")
		return
	default:
		writeError(c, http.StatusBadRequest, CodeUnsupportedFile, "Unsupported upload type. Supported file extensions: .pdf, .docx, .html, .htm, .md, .txt")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": Document{
		Markdown: markdown,
		Metadata: Metadata{SourceURL: "", URL: header.Filename, StatusCode: 200, ContentType: "text/plain"},
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
