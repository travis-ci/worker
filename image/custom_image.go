package image

type CustomImage struct {
	Id    int    `json:"id"`
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
}

type Owner struct {
	Id    int    `json:"id"`
	Type  string `json:"type"`
	Login string `json:"login"`
}
