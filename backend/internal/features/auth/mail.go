package auth

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const smtpDialTimeout = 10 * time.Second

// purposeTitle is the heading of a verification mail.
func purposeTitle(purpose string) string {
	switch purpose {
	case PurposeRegister:
		return "Create your account"
	case PurposePasswordChange:
		return "Confirm password change"
	case PurposePasswordReset:
		return "Reset your password"
	case PurposeAccountDelete:
		return "Confirm account deletion"
	default:
		return "Email verification"
	}
}

// buildVerificationMail renders the verification message for a purpose.
func (s *Service) buildVerificationMail(to, purpose, code string) Mail {
	title := purposeTitle(purpose)
	text := fmt.Sprintf("Riochat %s\n\nYour verification code: %s\n\nThis code expires in 10 minutes.\n\nIf you did not request this, please ignore this email, and never share the code with anyone.\n", title, code)

	logo := "Riochat"
	if base := strings.TrimRight(strings.TrimSpace(s.cfg().Server.PublicWebBaseURL), "/"); base != "" {
		logo = fmt.Sprintf(`<img src="%s/logo.svg" alt="Riochat" height="32">`, html.EscapeString(base))
	}
	page := fmt.Sprintf(`<!doctype html><html><body style="font-family:sans-serif;color:#222">
<div style="max-width:480px;margin:0 auto;padding:24px">
<div style="font-size:20px;font-weight:600;margin-bottom:16px">%s</div>
<h2 style="margin:0 0 12px">%s</h2>
<p>Your verification code:</p>
<p style="font-size:32px;letter-spacing:6px;font-weight:700;margin:8px 0 16px">%s</p>
<p>This code expires in 10 minutes.</p>
<p style="color:#666;font-size:13px">If you did not request this, please ignore this email, and never share the code with anyone.</p>
</div></body></html>`, logo, html.EscapeString(title), html.EscapeString(code))
	return Mail{To: to, Subject: "Your Riochat verification code", Text: text, HTML: page}
}

func (s *Service) mailer() Mailer {
	if s.h.Mailer != nil {
		return s.h.Mailer
	}
	return smtpMailer{svc: s}
}

// smtpMailer delivers mail through the SMTP server named in the settings.
type smtpMailer struct{ svc *Service }

// Send implements Mailer.
func (m smtpMailer) Send(ctx context.Context, msg Mail) error {
	st := m.svc.settings(ctx)
	host := strings.TrimSpace(st.SMTPHost)
	fromRaw := strings.TrimSpace(st.SMTPFrom)
	if fromRaw == "" {
		fromRaw = strings.TrimSpace(st.SMTPUsername)
	}
	if host == "" || fromRaw == "" {
		return ErrSMTPInvalid
	}
	from, err := mail.ParseAddress(fromRaw)
	if err != nil {
		return ErrSMTPInvalid.WithMessage("smtp from is invalid")
	}
	to, err := normalizeEmail(msg.To)
	if err != nil {
		return err
	}
	port := st.SMTPPort
	if port <= 0 {
		port = 587
	}
	data, err := composeMessage(from, to, msg)
	if err != nil {
		return ErrBadRequest.WithCause(err)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	dctx, cancel := context.WithTimeout(ctx, smtpDialTimeout)
	defer cancel()

	var conn net.Conn
	if port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsCfg}).DialContext(dctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(dctx, "tcp", addr)
	}
	if err != nil {
		return ErrBadRequest.WithCause(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * smtpDialTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return ErrBadRequest.WithCause(err)
	}
	defer client.Close()

	if port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return ErrBadRequest.WithCause(err)
			}
		}
	}
	if st.SMTPUsername != "" || st.SMTPPassword != "" {
		if ok, _ := client.Extension("AUTH"); !ok {
			return ErrSMTPInvalid.WithMessage("smtp auth is not supported")
		}
		if err := client.Auth(smtp.PlainAuth("", st.SMTPUsername, st.SMTPPassword, host)); err != nil {
			return ErrBadRequest.WithCause(err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return ErrBadRequest.WithCause(err)
	}
	if err := client.Rcpt(to); err != nil {
		return ErrBadRequest.WithCause(err)
	}
	w, err := client.Data()
	if err != nil {
		return ErrBadRequest.WithCause(err)
	}
	if _, err := w.Write(data); err != nil {
		return ErrBadRequest.WithCause(err)
	}
	if err := w.Close(); err != nil {
		return ErrBadRequest.WithCause(err)
	}
	_ = client.Quit()
	return nil
}

func wrap76(s string) string {
	var sb strings.Builder
	for len(s) > 76 {
		sb.WriteString(s[:76])
		sb.WriteString("\r\n")
		s = s[76:]
	}
	sb.WriteString(s)
	return sb.String()
}

// composeMessage renders a multipart/alternative message.
func composeMessage(from *mail.Address, to string, m Mail) ([]byte, error) {
	boundary := "riochat-" + randomHex(12)
	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	hdr("From", from.String())
	hdr("To", to)
	hdr("Subject", mime.QEncoding.Encode("UTF-8", m.Subject))
	hdr("Date", time.Now().UTC().Format(time.RFC1123Z))
	hdr("Message-ID", fmt.Sprintf("<%s@riochat>", randomHex(12)))
	hdr("MIME-Version", "1.0")
	hdr("Content-Type", fmt.Sprintf(`multipart/alternative; boundary="%s"`, boundary))
	b.WriteString("\r\n")
	part := func(ctype, body string) {
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", ctype)
		b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		b.WriteString(wrap76(base64.StdEncoding.EncodeToString([]byte(body))))
		b.WriteString("\r\n")
	}
	part("text/plain", m.Text)
	part("text/html", m.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}
