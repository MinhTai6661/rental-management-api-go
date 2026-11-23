package httpadapter

import domain "rental-management/internal/domain/location"

// DivisionView is a province or a ward as the API lists it. The json tags are the contract with the web app.
type DivisionView struct {
	Code int32  `json:"code"`
	Name string `json:"name"`
}

// divisionViewsOf is never nil, so an empty list is `[]` in the JSON, not null.
func divisionViewsOf(items []domain.Division) []DivisionView {
	views := make([]DivisionView, len(items))
	for i, d := range items {
		views[i] = DivisionView{Code: d.Code, Name: d.Name}
	}
	return views
}
