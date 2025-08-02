package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"rsc.io/pdf"
)

func ChunkDocument(bookID uint, filePath string) (int, error) {
	text, err := ExtractTextByType(filePath)
	if err != nil {
		return 0, err
	}

	runes := []rune(text)
	chunkSize := 1000
	total := len(runes)
	count := 0

	for i := 0; i < total; i += chunkSize {
		end := i + chunkSize
		if end > total {
			end = total
		}
		chunk := BookChunk{
			BookID:    bookID,
			Index:     count,
			Content:   string(runes[i:end]),
			AudioPath: "",
		}
		db.Create(&chunk)
		count++
	}

	return count, nil
}

func ExtractTextByType(path string) (string, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".pdf"):
		return ExtractTextFromPDF(path)
	case strings.HasSuffix(strings.ToLower(path), ".txt"):
		return ExtractTextFromTXT(path)
	case strings.HasSuffix(strings.ToLower(path), ".epub"):
		return ExtractTextFromEPUB(path)
	default:
		return "", errors.New("unsupported file type")
	}
}

// Add ExtractTextFromPDF, ExtractTextFromTXT, ExtractTextFromEPUB...
// You may already have this in utils — import and call it
func cleanUTF8(input []byte) string {
	// Your existing clean function goes here
	return string(input) // Replace this with your actual implementation
}

func ExtractTextFromTXT(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return cleanUTF8(data), nil
}

func ExtractTextFromPDF(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, _ := file.Stat()
	reader, err := pdf.NewReader(file, stat.Size())
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		content := page.Content()
		for _, text := range content.Text {
			buf.WriteString(text.S)
			buf.WriteString(" ")
		}
	}

	return buf.String(), nil
}

func ExtractTextFromEPUB(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var sb strings.Builder

	for _, f := range r.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".xhtml") || strings.HasSuffix(strings.ToLower(f.Name), ".html") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			sb.WriteString(string(content))
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}
