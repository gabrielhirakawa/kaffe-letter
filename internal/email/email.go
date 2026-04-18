package email

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"mime/quotedprintable"
	"net/smtp"
	"net/textproto"
	"strings"
)

type Sender struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func (s Sender) Send(to []string, subject, textBody, htmlBody string) error {
	var msg bytes.Buffer
	writer := multipart.NewWriter(&msg)
	boundary := writer.Boundary()

	headers := map[string]string{
		"From":         s.From,
		"To":           strings.Join(to, ", "),
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": fmt.Sprintf("multipart/alternative; boundary=%q", boundary),
	}
	for k, v := range headers {
		_, _ = fmt.Fprintf(&msg, "%s: %s\r\n", k, v)
	}
	_, _ = fmt.Fprint(&msg, "\r\n")

	textHeader := textproto.MIMEHeader{}
	textHeader.Set("Content-Type", "text/plain; charset=UTF-8")
	textHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	textPart, err := writer.CreatePart(textHeader)
	if err != nil {
		return err
	}
	qpText := quotedprintable.NewWriter(textPart)
	if _, err := qpText.Write([]byte(textBody)); err != nil {
		return err
	}
	_ = qpText.Close()

	htmlHeader := textproto.MIMEHeader{}
	htmlHeader.Set("Content-Type", "text/html; charset=UTF-8")
	htmlHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return err
	}
	qpHTML := quotedprintable.NewWriter(htmlPart)
	if _, err := qpHTML.Write([]byte(htmlBody)); err != nil {
		return err
	}
	_ = qpHTML.Close()

	if err := writer.Close(); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	auth := smtp.PlainAuth("", s.User, s.Pass, s.Host)
	return smtp.SendMail(addr, auth, s.From, to, msg.Bytes())
}
