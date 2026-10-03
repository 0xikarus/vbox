package controller

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseInboundMIMEAlternativeAttachmentAndAuthFailure(t *testing.T) {
	attachment := base64.StdEncoding.EncodeToString([]byte("plain file"))
	raw := "From: =?UTF-8?Q?Sophie?= <sophie@example.test>\r\nSubject: =?UTF-8?Q?Your_code?=\r\nAuthentication-Results: mx.example; spf=fail smtp.mailfrom=example.test; dkim=pass header.d=example.test\r\nContent-Type: multipart/mixed; boundary=outer\r\n\r\n" +
		"--outer\r\nContent-Type: multipart/alternative; boundary=inner\r\n\r\n" +
		"--inner\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nYour code is 123456. Visit https://example.test/secret\r\n" +
		"--inner\r\nContent-Type: text/html\r\n\r\n<script>ignore me</script><p>HTML fallback</p>\r\n--inner--\r\n" +
		"--outer\r\nContent-Type: text/plain; name=notes.txt\r\nContent-Disposition: attachment; filename=../notes.txt\r\nContent-Transfer-Encoding: base64\r\n\r\n" + attachment + "\r\n--outer--\r\n"
	parsed, err := parseInboundMIME([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.From != "sophie@example.test" || parsed.FromName != "Sophie" || parsed.Subject != "Your code" || parsed.SPF != "fail" || parsed.DKIM != "pass" || !parsed.Quarantined {
		t.Fatalf("headers=%+v", parsed)
	}
	if !strings.Contains(parsed.Text, "123456") || strings.Contains(parsed.Text, "HTML fallback") || strings.Contains(parsed.Preview, "123456") || strings.Contains(parsed.Preview, "https://") {
		t.Fatalf("text/preview=%q / %q", parsed.Text, parsed.Preview)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Name != "notes.txt" || string(parsed.Attachments[0].Data) != "plain file" || parsed.Attachments[0].Blocked {
		t.Fatalf("attachment=%+v", parsed.Attachments)
	}
}

func TestParseInboundMIMEHTMLFallbackAndAttachmentLimit(t *testing.T) {
	raw := []byte("From: sender@example.test\r\nContent-Type: text/html\r\n\r\n<style>hide</style><p>Hello &amp; welcome</p><br>Next")
	parsed, err := parseInboundMIME(raw)
	if err != nil || !strings.Contains(parsed.Text, "Hello & welcome") || strings.Contains(parsed.Text, "hide") {
		t.Fatalf("html fallback=%+v err=%v", parsed, err)
	}
	huge := bytes.Repeat([]byte("x"), maxStoredMailAttachments+1)
	data := base64.StdEncoding.EncodeToString(huge)
	raw = []byte("From: sender@example.test\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nHello\r\n--x\r\nContent-Type: text/plain; name=huge.txt\r\nContent-Disposition: attachment; filename=huge.txt\r\nContent-Transfer-Encoding: base64\r\n\r\n" + data + "\r\n--x--\r\n")
	parsed, err = parseInboundMIME(raw)
	if err != nil || len(parsed.Attachments) != 0 || !strings.Contains(parsed.Text, "omitted") {
		t.Fatalf("large attachment should be accepted and omitted: attachments=%d text=%q err=%v", len(parsed.Attachments), parsed.Text, err)
	}
}
