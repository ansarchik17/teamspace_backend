package domain

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

var contactPhonePattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// Normalize preserves omitted fields; an empty string explicitly clears a field.
func (r UpdateContactRequest) Normalize() (UpdateContactRequest, error) {
	if r.WorkEmail != nil {
		email := strings.TrimSpace(*r.WorkEmail)
		if email != "" {
			if !ValidContactEmail(email) {
				return r, fmt.Errorf("invalid work_email")
			}
		}
		r.WorkEmail = &email
	}
	if r.Phone != nil {
		phone := strings.TrimSpace(*r.Phone)
		if phone != "" {
			phone = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(phone)
			if !contactPhonePattern.MatchString(phone) {
				return r, fmt.Errorf("invalid phone: use international format + followed by 7 to 15 digits")
			}
		}
		r.Phone = &phone
	}
	for field, visibility := range map[string]*ContactVisibility{"personal_email_visibility": r.PersonalEmailVisibility, "personal_phone_visibility": r.PersonalPhoneVisibility} {
		if visibility != nil && !visibility.Valid() {
			return r, fmt.Errorf("invalid %s: use private, admins, managers or company", field)
		}
	}
	// Reuse exactly the same validation as work contacts, including explicit clearing.
	if r.PersonalEmail != nil || r.PersonalPhone != nil {
		personal, err := (UpdateContactRequest{WorkEmail: r.PersonalEmail, Phone: r.PersonalPhone}).Normalize()
		if err != nil {
			return r, fmt.Errorf("invalid personal contact: %w", err)
		}
		r.PersonalEmail, r.PersonalPhone = personal.WorkEmail, personal.Phone
	}
	return r, nil
}

// ValidContactEmail accepts exactly one address, without display names or header characters.
func ValidContactEmail(email string) bool {
	if email == "" || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}
