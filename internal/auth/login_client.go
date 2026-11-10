package auth

import "context"

type LoginClient string

const (
	LoginClientUnknown LoginClient = ""
	LoginClientWeb     LoginClient = "web"
	LoginClientMobile  LoginClient = "mobile"
)

type loginClientContextKey struct{}

func WithLoginClient(ctx context.Context, client LoginClient) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loginClientContextKey{}, client)
}

func LoginClientFromContext(ctx context.Context) LoginClient {
	if ctx == nil {
		return LoginClientUnknown
	}
	client, _ := ctx.Value(loginClientContextKey{}).(LoginClient)
	return client
}

func IsWebLoginClient(ctx context.Context) bool {
	return LoginClientFromContext(ctx) == LoginClientWeb
}
