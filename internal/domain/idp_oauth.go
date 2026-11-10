package domain

type IDPAuthURLResponse struct {
	AuthURL string `json:"auth_url"`
}

type EgovStartResponse struct {
	RedirectURL string `json:"redirectUrl"`
}

type EgovCallbackResponse struct {
	AccessToken  string                 `json:"accessToken,omitempty"`
	RefreshToken string                 `json:"refreshToken,omitempty"`
	TokenType    string                 `json:"tokenType,omitempty"`
	ExpiresIn    int64                  `json:"expiresIn,omitempty"`
	User         *EgovAuthenticatedUser `json:"user,omitempty"`
	IsNewUser    bool                   `json:"isNewUser"`
}

type EgovAuthenticatedUser struct {
	ID         int64  `json:"id"`
	IIN        string `json:"iin"`
	Phone      string `json:"phone,omitempty"`
	Email      string `json:"email,omitempty"`
	FirstName  string `json:"firstName"`
	LastName   string `json:"lastName"`
	Patronymic string `json:"patronymic,omitempty"`
	Type       string `json:"type"`
	AuthLevel  string `json:"authLevel"`
}

type IDPExchangeTokenRequest struct {
	Code        string `json:"code" binding:"required"`
	RedirectURI string `json:"redirect_uri,omitempty"`
}

type IDPRefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type IDPTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

type IDPUserBasicResponse struct {
	Person       IDPPerson        `json:"person"`
	Organization *IDPOrganization `json:"organization,omitempty"`
	Level        IDPAuthLevel     `json:"level"`
	Type         IDPUserType      `json:"type"`
}

type IDPPerson struct {
	IIN        string   `json:"iin"`
	Surname    string   `json:"surname"`
	Name       string   `json:"name"`
	Patronymic string   `json:"patronymic"`
	BirthDate  string   `json:"birthDate"`
	KeyUsage   []string `json:"keyUsage,omitempty"`
}

type IDPOrganization struct {
	BIN    string `json:"bin"`
	NameRu string `json:"nameRu"`
	NameKk string `json:"nameKk"`
}

type IDPUserPhoneResponse struct {
	Phone string `json:"phone"`
}

type IDPUserEmailResponse struct {
	Email string `json:"email"`
}

type IDPAuthLevel string

const (
	IDPAuthLevelPassword          IDPAuthLevel = "PASSWORD"
	IDPAuthLevelCertificate       IDPAuthLevel = "CERTIFICATE"
	IDPAuthLevelOTP               IDPAuthLevel = "OTP"
	IDPAuthLevelMobileCertificate IDPAuthLevel = "MOBILE_CERTIFICATE"
)

func (v IDPAuthLevel) Valid() bool {
	switch v {
	case IDPAuthLevelPassword, IDPAuthLevelCertificate, IDPAuthLevelOTP, IDPAuthLevelMobileCertificate:
		return true
	default:
		return false
	}
}

type IDPUserType string

const (
	IDPUserTypeIndividual IDPUserType = "INDIVIDUAL"
	IDPUserTypeLegal      IDPUserType = "LEGAL"
)

func (v IDPUserType) Valid() bool {
	switch v {
	case IDPUserTypeIndividual, IDPUserTypeLegal:
		return true
	default:
		return false
	}
}
