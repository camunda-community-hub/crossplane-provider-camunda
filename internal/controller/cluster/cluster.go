/*
Copyright 2020 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/pkg/errors"
	"golang.org/x/oauth2/clientcredentials"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane/provider-camunda/apis/cluster/v1alpha1"
	apisv1alpha1 "github.com/crossplane/provider-camunda/apis/v1alpha1"
	"github.com/crossplane/provider-camunda/internal/controller/features"
	console "github.com/sijoma/console-customer-api-go"
)

const (
	errNotMyType    = "managed resource is not a cluster custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// CamundaService connects to Camunda Cloud
type CamundaService struct {
	console.APIClient
	accessToken string
}

var (
	camundaService = func(ctx context.Context, creds []byte) (*CamundaService, error) {
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

		return &CamundaService{*client, token.AccessToken}, nil
	}
)

// Setup adds a controller that reconciles MyType managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.ClusterGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.ClusterGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: camundaService}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&v1alpha1.Cluster{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(ctx context.Context, creds []byte) (*CamundaService, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Cluster)
	if !ok {
		return nil, errors.New(errNotMyType)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(ctx, data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &external{service: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	service *CamundaService
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Cluster)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotMyType)
	}

	clusterName := cr.GetName()

	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.accessToken)
	inline, _, err := c.service.APIClient.ClustersApi.GetCluster(ctx, meta.GetExternalName(cr)).Execute()
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists:   false,
			ResourceUpToDate: false,
		}, nil
	}

	fmt.Println("cluster status", inline.Status)
	fmt.Println("cluster links", inline.Links)

	if inline.Status.ZeebeStatus != nil {
		switch *inline.Status.ZeebeStatus {
		case console.HEALTHY:
			cr.Status.SetConditions(xpv1.Available())
		case console.CREATING:
			cr.Status.SetConditions(xpv1.Creating())
		default:
			cr.Status.SetConditions(xpv1.Unavailable())
		}

	}

	connectionDetails := managed.ConnectionDetails{}
	// TODO: Proper check of all nil-pointers
	if inline.Links.Operate != nil {
		connectionDetails["operate"] = []byte(*inline.Links.Operate)
		connectionDetails["optimize"] = []byte(*inline.Links.Optimize)
		connectionDetails["tasklist"] = []byte(*inline.Links.Tasklist)
		connectionDetails["zeebe"] = []byte(*inline.Links.Zeebe)
	}

	return managed.ExternalObservation{
		// Return false when the external resource does not exist. This lets
		// the managed resource reconciler know that it needs to call Create to
		// (re)create the resource, or that it has successfully been deleted.
		ResourceExists: true,

		// Return false when the external resource exists, but it not up to date
		// with the desired managed resource state. This lets the managed
		// resource reconciler know that it needs to call Update.
		ResourceUpToDate: inline.Name == clusterName,

		// Return any details that may be required to connect to the external
		// resource. These will be stored as the connection secret.
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Cluster)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotMyType)
	}

	newClusterConfiguration := console.CreateClusterBody{
		Name:         cr.GetName(),
		PlanTypeId:   cr.Spec.ForProvider.PlanType,
		ChannelId:    cr.Spec.ForProvider.Channel,
		GenerationId: cr.Spec.ForProvider.Generation,
		RegionId:     cr.Spec.ForProvider.Region,
	}
	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.accessToken)
	inline, _, err := c.service.APIClient.ClustersApi.CreateCluster(ctx).
		CreateClusterBody(newClusterConfiguration).
		Execute()

	if err != nil {
		fmt.Println("the error", string(err.(*console.GenericOpenAPIError).Body()))
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, inline.GetClusterId())

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Cluster)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotMyType)
	}

	fmt.Printf("Updating: %+v", cr)

	return managed.ExternalUpdate{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Cluster)
	if !ok {
		return errors.New(errNotMyType)
	}

	fmt.Printf("Deleting: %+v", cr)

	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.accessToken)
	resp, err := c.service.APIClient.ClustersApi.DeleteCluster(ctx, meta.GetExternalName(cr)).Execute()

	if err != nil {
		fmt.Println("the resp", resp)
		fmt.Println("the error", string(err.(*console.GenericOpenAPIError).Body()))
	}

	return nil
}
