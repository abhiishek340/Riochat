package billing

import (
	"crypto/md5" //nolint:gosec // the EPay protocol mandates MD5 signatures
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// normalizeEpayGateway turns the configured gateway address into the
// submit.php endpoint. The address must be plain http(s) with a host and
// without credentials, query or fragment.
func normalizeEpayGateway(raw string) (string, error) {
	u, err := httpURL(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return "", errors.New("gateway must not carry a query or fragment")
	}
	path := u.Path
	segs := strings.Split(strings.TrimRight(path, "/"), "/")
	if last := segs[len(segs)-1]; strings.EqualFold(last, "submit.php") {
		u.Path = strings.TrimRight(path, "/")
	} else {
		u.Path = strings.TrimRight(path, "/") + "/submit.php"
	}
	u.RawPath = ""
	return u.String(), nil
}

// epaySign computes the MD5 signature: sorted non-blank parameters except
// sign and sign_type, joined k=v with &, the merchant key appended directly.
func epaySign(params map[string]string, key string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || strings.TrimSpace(v) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+params[k])
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + key)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// verifyEpay checks the signature of an incoming notification in constant time.
func verifyEpay(params map[string]string, key string) bool {
	if key == "" {
		return false
	}
	got := strings.ToLower(strings.TrimSpace(params["sign"]))
	if got == "" {
		return false
	}
	want := epaySign(params, key)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// formatMoney renders minor units as X.YY.
func formatMoney(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// moneyMatches reports whether the notified amount equals the order's pay
// amount, textually or numerically (rounded half up to cents).
func moneyMatches(notified string, payCents int64) bool {
	notified = strings.TrimSpace(notified)
	if notified == formatMoney(payCents) {
		return true
	}
	f, err := strconv.ParseFloat(notified, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return int64(math.Floor(f*100+0.5)) == payCents
}

type epayParams struct {
	Gateway     string // as configured
	PID         string
	Key         string
	Type        string
	OrderNo     string
	NotifyURL   string
	ReturnURL   string
	Name        string
	PayCurrency string
	PayCents    int64
}

// buildEpayURL assembles the signed redirect URL.
func buildEpayURL(p epayParams) (string, error) {
	gw, err := normalizeEpayGateway(p.Gateway)
	if err != nil {
		return "", ErrEpayGatewayInvalid
	}
	switch {
	case p.PayCurrency != CurrencyCNY, p.PayCents <= 0, p.OrderNo == "", p.PID == "", p.Key == "",
		!validEpayTypeCode(p.Type), strings.TrimSpace(p.Name) == "":
		return "", ErrProviderUnavailable.WithStatus(503)
	}
	for _, raw := range []string{p.NotifyURL, p.ReturnURL} {
		if _, err := httpURL(raw); err != nil {
			return "", ErrProviderUnavailable.WithStatus(503)
		}
	}
	params := map[string]string{
		"pid": p.PID, "type": p.Type, "out_trade_no": p.OrderNo, "notify_url": p.NotifyURL, "return_url": p.ReturnURL,
		"name": p.Name, "money": formatMoney(p.PayCents),
	}
	sign := epaySign(params, p.Key)
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	q.Set("sign", sign)
	q.Set("sign_type", "MD5")
	return gw + "?" + q.Encode(), nil
}
