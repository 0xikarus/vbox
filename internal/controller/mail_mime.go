package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type parsedMailAttachment struct {
	Name        string
	ContentType string
	Data        []byte
	SHA256      string
	Blocked     bool
}
type parsedInboundMail struct {
	MessageID   string
	From        string
	FromName    string
	Subject     string
	Text        string
	Preview     string
	SPF         string
	DKIM        string
	Quarantined bool
	Attachments []parsedMailAttachment
}
type mimeCollector struct {
	plain       string
	html        string
	attachments []parsedMailAttachment
	parts       int
	bytes       int
	storedBytes int
	omitted     int
}

const maxStoredMailAttachments = 10 << 20

var mailTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
var mailScriptPattern = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)\s*>`)
var mailBreakPattern = regexp.MustCompile(`(?i)<(?:br|/p|/div|/li)\b[^>]*>`)
var mailOTPPattern = regexp.MustCompile(`\b[0-9]{4,8}\b`)
var mailURLPattern = regexp.MustCompile(`https?://[^\s<>]+`)
var authSPFPattern = regexp.MustCompile(`(?i)\bspf=(pass|fail|softfail|neutral|none|temperror|permerror)\b`)
var authDKIMPattern = regexp.MustCompile(`(?i)\bdkim=(pass|fail|neutral|none|temperror|permerror)\b`)

func parseInboundMIME(raw []byte) (parsedInboundMail, error) {
	var value parsedInboundMail
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return value, err
	}
	decoded := func(text string) string {
		result, err := new(mime.WordDecoder).DecodeHeader(text)
		if err == nil {
			return result
		}
		return text
	}
	value.MessageID = boundedMailText(message.Header.Get("Message-Id"), 254)
	value.Subject = boundedMailText(decoded(message.Header.Get("Subject")), 200)
	from := decoded(message.Header.Get("From"))
	address, err := mail.ParseAddress(from)
	if err == nil {
		value.From = boundedMailText(address.Address, 254)
		value.FromName = boundedMailText(address.Name, 200)
	} else {
		value.From = boundedMailText(from, 254)
	}
	authHeaders := textproto.MIMEHeader(message.Header).Values("Authentication-Results")
	value.SPF = parseAuthVerdict(authHeaders, authSPFPattern)
	value.DKIM = parseAuthVerdict(authHeaders, authDKIMPattern)
	value.Quarantined = value.SPF == "fail" || value.SPF == "softfail" || value.SPF == "permerror" || value.DKIM == "fail" || value.DKIM == "permerror"
	collector := mimeCollector{}
	if err := collector.readPart(textproto.MIMEHeader(message.Header), message.Body, 0); err != nil {
		return value, err
	}
	value.Text = collector.plain
	if value.Text == "" {
		value.Text = htmlMailText(collector.html)
	}
	if len(value.Text) > 1<<20 {
		value.Text = value.Text[:1<<20]
	}
	value.Text = strings.ToValidUTF8(value.Text, "�")
	if collector.omitted > 0 {
		value.Text += fmt.Sprintf("\n[%d attachment(s) omitted: storage limit]", collector.omitted)
	}
	value.Preview = mailPreview(value.Text)
	value.Attachments = collector.attachments
	return value, nil
}

func parseAuthVerdict(headers []string, pattern *regexp.Regexp) string {
	result := "unknown"
	for _, header := range headers {
		for _, match := range pattern.FindAllStringSubmatch(header, -1) {
			verdict := strings.ToLower(match[1])
			if verdict == "fail" || verdict == "softfail" || verdict == "permerror" {
				return verdict
			}
			if result == "unknown" {
				result = verdict
			}
		}
	}
	return result
}

func (c *mimeCollector) readPart(header textproto.MIMEHeader, body io.Reader, depth int) error {
	if depth > 8 || c.parts >= 100 {
		return fmt.Errorf("MIME nesting or part limit exceeded")
	}
	c.parts++
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("invalid MIME content type")
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return fmt.Errorf("multipart boundary missing")
		}
		parts := multipart.NewReader(body, boundary)
		for {
			part, err := parts.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("invalid multipart body")
			}
			if err := c.readPart(part.Header, part, depth+1); err != nil {
				part.Close()
				return err
			}
			part.Close()
		}
		return nil
	}
	var decoded io.Reader = body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	case "", "7bit", "8bit", "binary":
	default:
		return fmt.Errorf("unsupported content transfer encoding")
	}
	data, err := io.ReadAll(io.LimitReader(decoded, int64(maxInboundMailBytes-c.bytes)+1))
	if err != nil {
		return fmt.Errorf("invalid MIME encoding")
	}
	c.bytes += len(data)
	if c.bytes > maxInboundMailBytes {
		return fmt.Errorf("MIME decoded size limit exceeded")
	}
	disposition, dispositionParams, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := dispositionParams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if filename != "" || disposition == "attachment" || !(mediaType == "text/plain" || mediaType == "text/html") {
		if c.storedBytes+len(data) > maxStoredMailAttachments {
			c.omitted++
			return nil
		}
		c.storedBytes += len(data)
		filename = safeMailFilename(filename)
		digest := sha256.Sum256(data)
		blocked := !allowedMailAttachment(mediaType, data)
		c.attachments = append(c.attachments, parsedMailAttachment{Name: filename, ContentType: mediaType, Data: data, SHA256: hex.EncodeToString(digest[:]), Blocked: blocked})
		return nil
	}
	text := decodeMailCharset(data, params["charset"])
	if mediaType == "text/plain" && c.plain == "" {
		c.plain = text
	}
	if mediaType == "text/html" && c.html == "" {
		c.html = text
	}
	return nil
}

func safeMailFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = boundedMailText(name, 120)
	if name == "" || name == "." || name == ".." {
		return "attachment"
	}
	return name
}
func allowedMailAttachment(mediaType string, data []byte) bool {
	switch mediaType {
	case "text/plain", "text/csv":
		return utf8.Valid(data)
	case "application/pdf":
		return bytes.HasPrefix(data, []byte("%PDF-"))
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return http.DetectContentType(data) == mediaType
	default:
		return false
	}
}
func decodeMailCharset(data []byte, charset string) string {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "iso-8859-1", "latin1":
		var b strings.Builder
		for _, value := range data {
			b.WriteRune(rune(value))
		}
		return b.String()
	default:
		return strings.ToValidUTF8(string(data), "�")
	}
}
func htmlMailText(value string) string {
	value = mailScriptPattern.ReplaceAllString(value, "")
	value = mailBreakPattern.ReplaceAllString(value, "\n")
	value = mailTagPattern.ReplaceAllString(value, "")
	return strings.TrimSpace(html.UnescapeString(value))
}
func boundedMailText(value string, maxRunes int) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			continue
		}
		b.WriteRune(r)
		maxRunes--
		if maxRunes == 0 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
func mailPreview(value string) string {
	value = mailURLPattern.ReplaceAllString(value, "[link]")
	value = mailOTPPattern.ReplaceAllString(value, "••••")
	value = strings.Join(strings.Fields(value), " ")
	return boundedMailText(value, 180)
}
