package auth

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

var googleOauthConfig *oauth2.Config

func InitGoogleAuth() {
	googleOauthConfig = &oauth2.Config{
		RedirectURL:  os.Getenv("GOOGLE_REDIRECT_URI"),
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		Endpoint: google.Endpoint,
	}
}

func GetGoogleLoginURL(state string) string {
	return googleOauthConfig.AuthCodeURL(state)
}

func GetGoogleUserInfo(code string) (*oauth2.Token, *oauth2.Userinfo, error) {
	token, err := googleOauthConfig.Exchange(context.Background(), code)
	if err != nil {
		return nil, nil, fmt.Errorf("code exchange failed: %s", err.Error())
	}

	oauth2Service, err := oauth2.NewService(context.Background(), option.WithTokenSource(googleOauthConfig.TokenSource(context.Background(), token)))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create oauth2 service: %s", err.Error())
	}

	userInfo, err := oauth2Service.Userinfo.Get().Do()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user info: %s", err.Error())
	}

	return token, userInfo, nil
}
