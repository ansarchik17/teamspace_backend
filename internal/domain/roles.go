package domain

type Role string

const (
	RoleSuperAdmin Role = "admin"
	RoleManager    Role = "manager"
	RoleUser       Role = "user"
	RoleGuest      Role = "guest"
)

func (r Role) String() string {
	return string(r)
}
