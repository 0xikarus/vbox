package v1

// Identity describes the authenticated controller principal, never its token.
type Identity struct {
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	UserID      string `json:"userId"`
	Subject     string `json:"subject"`
	Role        string `json:"role"`
}
