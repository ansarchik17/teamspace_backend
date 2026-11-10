package domain

type TwoSlidesGenerateRequest struct {
	Kind             string `json:"kind"` // fast_ppt | pdf_slides | like_this
	ThemeID          string `json:"themeId"`
	UserInput        string `json:"userInput" binding:"required"`
	ResponseLanguage string `json:"responseLanguage"`
	Mode             string `json:"mode"`

	DesignStyle       string `json:"designStyle"`
	ReferenceImageURL string `json:"referenceImageUrl"`
	ImageModel        string `json:"imageModel"`
	AspectRatio       string `json:"aspectRatio"`
	Resolution        string `json:"resolution"`
	Page              int    `json:"page"`
	ContentDetail     string `json:"contentDetail"`
}

type TwoSlidesThemeSearchRequest struct {
	Query string `form:"query" binding:"required"`
	Limit int    `form:"limit"`
}
