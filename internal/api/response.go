package api

// cobalt response types

type ErrorDetail struct {
	Code    string         `json:"code"`
	Context map[string]any `json:"context,omitempty"`
}

type ErrorResponse struct {
	Status string      `json:"status"`
	Error  ErrorDetail `json:"error"`
}

type RedirectResponse struct {
	Status   string `json:"status"`
	URL      string `json:"url"`
	Filename string `json:"filename,omitempty"`
}

type TunnelResponse struct {
	Status   string `json:"status"`
	URL      string `json:"url"`
	Filename string `json:"filename,omitempty"`
}

type PickerItem struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Thumb string `json:"thumb,omitempty"`
}

type PickerResponse struct {
	Status        string       `json:"status"`
	Picker        []PickerItem `json:"picker"`
	Audio         string       `json:"audio,omitempty"`
	AudioFilename string       `json:"audioFilename,omitempty"`
}

func NewError(code string, context map[string]any) ErrorResponse {
	return ErrorResponse{
		Status: "error",
		Error: ErrorDetail{
			Code:    code,
			Context: context,
		},
	}
}

func NewRedirect(url, filename string) RedirectResponse {
	return RedirectResponse{
		Status:   "redirect",
		URL:      url,
		Filename: filename,
	}
}

func NewPicker(items []PickerItem, audio, audioFilename string) PickerResponse {
	return PickerResponse{
		Status:        "picker",
		Picker:        items,
		Audio:         audio,
		AudioFilename: audioFilename,
	}
}

func NewTunnel(url, filename string) TunnelResponse {
	return TunnelResponse{
		Status:   "tunnel",
		URL:      url,
		Filename: filename,
	}
}
