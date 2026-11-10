package domain

// ContactVisibility controls personal contact access inside the employee's active company.
// The employee always sees their own data. Private excludes administrators too.
type ContactVisibility string

const (
	ContactPrivate  ContactVisibility = "private"
	ContactAdmins   ContactVisibility = "admins"
	ContactManagers ContactVisibility = "managers"
	ContactCompany  ContactVisibility = "company"
)

// ContactPrivacy is returned only to the employee who can change these settings.
type ContactPrivacy struct {
	PersonalEmailVisibility ContactVisibility `json:"personal_email_visibility" enums:"private,admins,managers,company"`
	PersonalPhoneVisibility ContactVisibility `json:"personal_phone_visibility" enums:"private,admins,managers,company"`
}

func (v ContactVisibility) Valid() bool {
	return v == ContactPrivate || v == ContactAdmins || v == ContactManagers || v == ContactCompany
}
