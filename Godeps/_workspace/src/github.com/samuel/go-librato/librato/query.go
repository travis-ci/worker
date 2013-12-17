package librato

type QueryResponse struct {
	Found  int `json:"found"`
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Length int `json:"length"`
}
