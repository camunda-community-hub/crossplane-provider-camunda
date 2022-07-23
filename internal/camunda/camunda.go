package camunda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	console "github.com/sijoma/console-customer-api-go"
	"golang.org/x/oauth2/clientcredentials"
)

// Service connects to Camunda Cloud
type Service struct {
	console.APIClient
	AccessToken string
}

// NewService creates a Camunda service to connect to Camunda Cloud
func NewService(ctx context.Context, creds []byte) (*Service, error) {
	camundaCreds := map[string]string{}
	if err := json.Unmarshal(creds, &camundaCreds); err != nil {
		return nil, err
	}
	config := clientcredentials.Config{
		ClientID:     camundaCreds["client_id"],
		ClientSecret: camundaCreds["client_secret"],
		TokenURL:     "https://login.cloud.camunda.io/oauth/token",
		EndpointParams: url.Values{
			"audience": []string{"api.cloud.camunda.io"},
		},
	}
	token, err := config.Token(ctx)
	if err != nil {
		fmt.Println("unable to fetch token" + err.Error())
		return nil, err
	}

	cfg := console.NewConfiguration()
	cfg.Scheme = "https"
	cfg.Host = "api.cloud.camunda.io"
	client := console.NewAPIClient(cfg)

	return &Service{*client, token.AccessToken}, nil
}
