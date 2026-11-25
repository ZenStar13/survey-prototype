package domain

import (
	"errors"
	"net/mail"
	"strings"
)

const maxEmailLength = 254

// Email is a normalised (trimmed, lower-cased) email address. Normalising is
// what makes "one response per email" hold for "Ann@X.com" vs "ann@x.com".
type Email struct{ value string }

func NewEmail(raw string) (Email, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return Email{}, errors.New("is required")
	}
	if len(s) > maxEmailLength {
		return Email{}, errors.New("is too long")
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return Email{}, errors.New("is not a valid email address")
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || !strings.Contains(s[at+1:], ".") {
		return Email{}, errors.New("is not a valid email address")
	}
	return Email{value: s}, nil
}

func (e Email) String() string { return e.value }
