package ip8s

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"text/template"

	"github.com/cloudflare/cloudflare-go"
	"github.com/nlopes/slack"
	"github.com/pkg/errors"
)

type Broadcaster interface {
	Broadcast(ctx context.Context, ips []string) error
}

type multiError []error

func (e multiError) Error() string {
	msgs := make([]string, len(e))
	for i, msg := range e {
		msgs[i] = msg.Error()
	}
	return strings.Join(msgs, "\n-----------------------\n")
}

type multiBroadcaster []Broadcaster

func (b multiBroadcaster) Broadcast(ctx context.Context, ips []string) error {
	w := sync.WaitGroup{}
	w.Add(len(b))
	errs := make([]error, 0)
	l := sync.Mutex{}
	for i := range b {
		go func(i int) {
			defer w.Done()
			if b[i] == nil {
				return
			}
			err := b[i].Broadcast(ctx, ips)
			if err != nil {
				l.Lock()
				defer l.Unlock()
				errs = append(errs, err)
			}
		}(i)
	}
	w.Wait()
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errors.Wrap(errs[0], "one broadcast failed")
	}
	return errors.Wrap(multiError(errs), "multiple error occured during broadcasting")
}

// type emailBroadcaster struct {
// 	auth    smtp.Auth
// 	server  string
// 	from    string
// 	dest    []string
// 	subject string
// 	body    string
// }
//
// var emailTmpl = `
// From: {{ .from }}
// To: {{ join ", " .dest }}
// Subject: {{ .subject }}
// MIME-Version: 1.0
// Content-Type: text/html; charset=utf-8
// Content-Transfer-Encoding: quoted-printable
// Content-Disposition: inline
//
// `
//
// func (b emailBroadcaster) buildContent(ips []string) (string, error) {
// 	builder := &strings.Builder{}
// 	appendToBuilder := func(label, content string) {
// 		builder.WriteString(label)
// 		builder.WriteString(": ")
// 		builder.WriteString(content)
// 		builder.WriteString("\r\n")
// 	}
// 	appendToBuilder("From", b.from)
// 	appendToBuilder("To", strings.Join(b.dest, ", "))
// 	appendToBuilder("Subject", b.subject)
// 	appendToBuilder("MIME-Version", "1.0")
// 	appendToBuilder("Content-Type", "text/html; charset\"utf-8\"")
// 	appendToBuilder("Content-Transfer-Encoding", "quoted-printable")
// 	appendToBuilder("Content-Disposition", "inline")
// 	builder.WriteString("\r\n")
// 	msgBuilder := quotedprintable.NewWriter(builder)
// 	msgBuilder.Write([]byte(fmt.Sprintf(b.body, ips)))
// 	if err := msgBuilder.Close(); err != nil {
// 		return "", errors.Wrap(err, "failed to quote the htlp content")
// 	}
// 	return builder.String(), nil
// }
//
// func (b emailBroadcaster) Broadcast(ctx context.Context, ips []string) error {
// 	msg, err := b.buildContent(ips)
// 	if err != nil {
// 		return err
// 	}
// 	msg = "From: " + b.from + "\n" +
// 		"To: " + strings.Join(b.dest, ",") + "\n" +
// 		"Subject: " + b.subject + "\n" +
// 		"\n" + msg
// 	err = smtp.SendMail(b.server, b.auth, b.from, b.dest, []byte(msg))
// 	if err != nil {
// 		return errors.Wrap(err, "failed to send mail")
// 	}
// 	return nil
// }

type slackBroadcaster struct {
	api     *slack.Client
	roomID  string
	dnsName string
}

func join(sep string, a []string) string {
	return strings.Join(a, sep)
}

var slackTmpl = `
{{- define "italic" -}}
_{{ join "\t" . }}_
{{- end -}}
IPs for {{ .DNSName }} {{ with .IPs }}changed:
{{ template "italic" . }}{{ else }}were deleted{{ end -}}
`

func execDefaultSlackTemplate(dns string, ips []string) (string, error) {
	tmpl, err := template.New("slack_tmpl").
		Funcs(map[string]interface{}{"join": join}).
		Parse(slackTmpl)
	if err != nil {
		return "", err
	}
	return execTemplate(tmpl, dns, ips)
}

func execTemplate(tmpl *template.Template, dns string, ips []string) (string, error) {
	b := &bytes.Buffer{}
	err := tmpl.Execute(b, map[string]interface{}{
		"DNSName": dns,
		"IPs":     ips,
	})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

func (b slackBroadcaster) newMessage(ips []string) slack.MsgOption {
	msg := fmt.Sprintf("IPs for %s ", b.dnsName)
	if len(ips) == 0 {
		msg += "were deleted"
	} else {
		ipsText := strings.Join(ips, "\t")
		msg += fmt.Sprintf("changed:\n_%s_", ipsText)
	}
	return slack.MsgOptionBlocks(
		slack.NewSectionBlock(
			slack.NewTextBlockObject(
				slack.MarkdownType,
				msg,
				false,
				false,
			),
			nil,
			nil,
		),
	)
}

func (b slackBroadcaster) Broadcast(ctx context.Context, ips []string) error {
	_, _, err := b.api.PostMessageContext(ctx, b.roomID, b.newMessage(ips))
	return err
}

type cloudflareDNSBroadcaster struct {
	api     *cloudflare.API
	dnsName string
}

func (b cloudflareDNSBroadcaster) splitIPs(ips []string, records []cloudflare.DNSRecord) (
	map[string]struct{}, map[string]cloudflare.DNSRecord, map[string]cloudflare.DNSRecord,
) {
	newIPs := map[string]struct{}{}
	commonIPs := map[string]cloudflare.DNSRecord{}
	oldIPs := map[string]cloudflare.DNSRecord{}
	for _, ip := range ips {
		newIPs[ip] = struct{}{}
	}
	for _, record := range records {
		ip := record.Content
		if _, exists := newIPs[ip]; exists {
			commonIPs[ip] = record
			delete(newIPs, ip)
		} else {
			oldIPs[ip] = record
		}
	}
	return newIPs, commonIPs, oldIPs
}

func (b cloudflareDNSBroadcaster) newRecord(ip string) cloudflare.DNSRecord {
	return cloudflare.DNSRecord{
		Type:    "A",
		Name:    b.dnsName,
		Content: ip,
	}
}

func (b cloudflareDNSBroadcaster) Broadcast(ctx context.Context, ips []string) error {
	zoneID, err := b.api.ZoneIDByName(b.dnsName)
	if err != nil {
		return errors.Wrapf(err, "unable to determine the zone for dns=%s", b.dnsName)
	}
	records, err := b.api.DNSRecords(zoneID, cloudflare.DNSRecord{Type: "A", Name: b.dnsName})
	if err != nil {
		return errors.Wrapf(err, "failed to fetch A-records for zoneID:%s,dns:%s", zoneID, b.dnsName)
	}
	newIPs, commonIPs, oldIPs := b.splitIPs(ips, records)
	for _, record := range oldIPs {
		id := record.ID
		if err := b.api.DeleteDNSRecord(zoneID, id); err != nil {
			return errors.Wrapf(err, "failed to delete a record id:%s", id)
		}
	}
	for ip, record := range commonIPs {
		id := record.ID
		if err := b.api.UpdateDNSRecord(zoneID, id, b.newRecord(ip)); err != nil {
			return errors.Wrapf(err, "failed to update a record id:%s", id)
		}
	}
	for ip := range newIPs {
		if _, err := b.api.CreateDNSRecord(zoneID, b.newRecord(ip)); err != nil {
			return errors.Wrapf(err, "failed to create a record ip:%s", ip)
		}
	}
	return nil
}
