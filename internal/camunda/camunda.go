package camunda

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/go-logr/logr"
	console "github.com/sijoma/console-customer-api-go"
	"golang.org/x/oauth2/clientcredentials"
)

const userAgent = "go/crossplane"

// Service connects to Camunda Cloud
type Service struct {
	console.APIClient
	AccessToken string
}

var camundaService *Service

// NewService creates a Camunda service to connect to Camunda Cloud
func NewService(ctx context.Context, creds []byte) (*Service, error) {
	log, _ := logr.FromContext(ctx)
	// Singleton to not refetch access token all the time
	// ToDo: This probably causes issues when the token is expired
	if camundaService != nil {
		return camundaService, nil
	}
	camundaCreds := map[string]string{}
	if err := json.Unmarshal(creds, &camundaCreds); err != nil {
		return nil, err
	}
	tokenUrl, ok := camundaCreds["token_url"]
	if !ok {
		tokenUrl = "https://login.cloud.camunda.io/oauth/token"
	}

	audience, ok := camundaCreds["audience"]
	if !ok {
		audience = "api.cloud.camunda.io"
	}
	config := clientcredentials.Config{
		ClientID:     camundaCreds["client_id"],
		ClientSecret: camundaCreds["client_secret"],
		TokenURL:     tokenUrl,
		EndpointParams: url.Values{
			"audience": []string{audience},
		},
	}
	token, err := config.Token(ctx)
	if err != nil {
		log.Error(err, "unable to fetch token for camunda provider")
		return nil, err
	}

	log.Info("Authenticated against Camunda API", "audience", audience, "tokenUrl", tokenUrl)

	cfg := console.NewConfiguration()
	cfg.Scheme = "https"
	cfg.Host = audience
	cfg.UserAgent = userAgent
	client := console.NewAPIClient(cfg)
	camundaService = &Service{APIClient: *client, AccessToken: token.AccessToken}
	return camundaService, err
}
