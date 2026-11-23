package httpadapter

// Views that more than one feature puts in its answers.

// WardView is the ward (and its province) of the user's address.
type WardView struct {
	Code     int32        `json:"code"`
	Name     string       `json:"name"`
	Province ProvinceView `json:"province"`
}

// ProvinceView is a province.
type ProvinceView struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}
