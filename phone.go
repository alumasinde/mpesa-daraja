package mpesa

import (
	"fmt"
	"strings"
)

// NormalizeMSISDN converts common Kenyan phone formats to the 2547XXXXXXXX /
// 2541XXXXXXXX form Daraja requires. Accepts 07XXXXXXXX, 01XXXXXXXX,
// 7XXXXXXXX, +254..., 254... and tolerates spaces and dashes.
func NormalizeMSISDN(s string) (string, error) {
	r := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	p := strings.TrimPrefix(r.Replace(s), "+")

	for _, c := range p {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("mpesa: invalid phone number %q", s)
		}
	}

	switch {
	case strings.HasPrefix(p, "254") && len(p) == 12:
		// already canonical
	case strings.HasPrefix(p, "0") && len(p) == 10:
		p = "254" + p[1:]
	case len(p) == 9 && (p[0] == '7' || p[0] == '1'):
		p = "254" + p
	default:
		return "", fmt.Errorf("mpesa: invalid phone number %q", s)
	}

	if p[3] != '7' && p[3] != '1' {
		return "", fmt.Errorf("mpesa: invalid phone number %q", s)
	}
	return p, nil
}
