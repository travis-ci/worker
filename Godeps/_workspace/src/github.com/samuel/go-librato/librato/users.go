package librato

import (
	"encoding/json"
	"errors"
)

type User struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	Password  string `json:"password"`
	APIToken  string `json:"api_token"`
	Reference string `json:"reference"`
	Name      string `json:"name"`
	Company   string `json:"company"`
	Country   string `json:"country"`
	TimeZone  string `json:"time_zone"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type UsersResponse struct {
	Query QueryResponse `json:"query"`
	Users []User        `json:"users"`
}

// Return all users managed by the partner.
// http://dev.librato.com/v1/get/users
func (met *Metrics) GetUsers(reference string, email string) (*UsersResponse, error) {
	res, err := met.get(metricsUsersApiUrl)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != 200 {
		return nil, errors.New(res.Status)
	}

	var users UsersResponse
	jdec := json.NewDecoder(res.Body)
	err = jdec.Decode(&users)
	return &users, err
}
