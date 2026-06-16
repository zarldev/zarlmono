package auth

import "context"

// Service defines the methods any user service must implement to
// satisfy the auth handlers.
type Service interface {
	Register(ctx context.Context, req RegisterRequest) (LoginResponse, error)
	Login(ctx context.Context, req LoginRequest) (LoginResponse, error)
	UserByID(ctx context.Context, userID UserID) (User, error)
}

// User is the minimum surface auth needs from a user record.
type User interface {
	ID() UserID
	Username() string
	Email() string
}

// RegisterRequest represents user registration data.
type RegisterRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginRequest represents user login data.
type LoginRequest struct {
	Login    string `json:"login"` // email or username
	Password string `json:"password"`
}

// LoginResponse represents a successful login or registration response.
type LoginResponse struct {
	User   User      `json:"user"`
	Tokens TokenPair `json:"tokens"`
}

// TokenRefreshResponse is returned by RefreshToken. CSRF is no longer
// returned in-band — see pkg/zhttp/middleware.CrossOrigin.
type TokenRefreshResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// MessageResponse represents a simple message response.
type MessageResponse struct {
	Message string `json:"message"`
}
