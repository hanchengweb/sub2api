package service

import (
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AccountTypePersonal            = "personal"
	AccountTypeOrganizationService = "organization_service"
)

var (
	ErrServiceAccountLogin    = infraerrors.Forbidden("SERVICE_ACCOUNT_LOGIN_DISABLED", "organization service accounts cannot sign in")
	ErrServiceAccountIdentity = infraerrors.BadRequest("ORGANIZATION_IDENTITY_INVALID", "invalid or incomplete organization service identity")
)

func (u *User) NormalizedAccountType() string {
	if u.AccountType == "" {
		return AccountTypePersonal
	}
	return u.AccountType
}

// CanLogin is independent of IsActive: service API keys must remain usable.
func (u *User) CanLogin() bool {
	return u != nil && u.NormalizedAccountType() == AccountTypePersonal
}

func validateOrganizationIdentity(input *CreateUserInput) (*User, error) {
	if input == nil {
		return nil, ErrServiceAccountIdentity
	}
	u := &User{
		AccountType:             strings.TrimSpace(input.AccountType),
		OrganizationIssuer:      strings.TrimSpace(input.OrganizationIssuer),
		OrganizationID:          strings.TrimSpace(input.OrganizationID),
		OrganizationEnvironment: strings.TrimSpace(input.OrganizationEnvironment),
	}
	u.AccountType = u.NormalizedAccountType()
	switch u.AccountType {
	case AccountTypePersonal:
		if u.OrganizationIssuer != "" || u.OrganizationID != "" || u.OrganizationEnvironment != "" || len(input.Password) < 6 {
			return nil, ErrServiceAccountIdentity
		}
	case AccountTypeOrganizationService:
		if u.OrganizationIssuer == "" || len(u.OrganizationIssuer) > 80 || u.OrganizationID == "" || len(u.OrganizationID) > 160 ||
			(u.OrganizationEnvironment != "test" && u.OrganizationEnvironment != "production") ||
			(input.Role != "" && input.Role != RoleUser) || input.Password != "" {
			return nil, ErrServiceAccountIdentity
		}
	default:
		return nil, ErrServiceAccountIdentity
	}
	return u, nil
}
