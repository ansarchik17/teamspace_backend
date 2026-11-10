package domain

import "testing"

func TestSuggestedEmployeeLoginMatchesWorkspaceForm(t *testing.T) {
	for _, c := range []struct{ first, last, want string }{
		{"Тест", "Сотрудник", "t.sotrudnik"}, {"Әлия", "Қасымова", "a.qasymova"},
		{"Жан", "Ёлкин", "z.yolkin"}, {" Émile ", "O'Neil", "e.o-neil"},
		{"", "Сотрудник", ""}, {"李", "王", ""},
	} {
		if got := SuggestedEmployeeLogin(c.first, c.last); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
func TestCompanyMailDomainRejectsForeignOrMalformedSlug(t *testing.T) {
	if got := CompanyMailDomain("sultan-corp", "teamspace.kz"); got != "sultan-corp.teamspace.kz" {
		t.Fatal(got)
	}
	for _, slug := range []string{"", "other.teamspace.kz", "a@foreign", "-bad", "../company"} {
		if CompanyMailDomain(slug, "teamspace.kz") != "" {
			t.Fatal(slug)
		}
	}
}
