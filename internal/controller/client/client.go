/*
Copyright 2022 The Crossplane Authors.

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

package client

import (
	"context"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	console "github.com/sijoma/console-customer-api-go"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/provider-camunda/internal/camunda"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/crossplane/provider-camunda/apis/client/v1alpha1"
	apisv1alpha1 "github.com/crossplane/provider-camunda/apis/v1alpha1"
	"github.com/crossplane/provider-camunda/internal/controller/features"
)

const (
	errNotclient    = "managed resource is not a client custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

// Setup adds a controller that reconciles client managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.ClientGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.ClientGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: camunda.NewService}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&v1alpha1.Client{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(ctx context.Context, creds []byte) (*camunda.Service, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Client)
	if !ok {
		return nil, errors.New(errNotclient)
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
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	service *camunda.Service
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Client)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotclient)
	}

	clientName := cr.GetName()
	clientId := meta.GetExternalName(cr)
	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.AccessToken)
	inline, _, err := c.service.ClustersApi.
		GetClient(ctx, cr.Spec.ForProvider.ClusterID, clientId).
		Execute()
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists:   false,
			ResourceUpToDate: false,
		}, nil
	}
	connectionDetails := managed.ConnectionDetails{}
	if inline.GetName() == clientName {
		cr.Status.SetConditions(xpv1.Available())
	} else {
		cr.Status.SetConditions(xpv1.Unavailable())
	}

	connectionDetails["ZEEBE_CLIENT_ID"] = []byte(inline.ZEEBE_CLIENT_ID)
	connectionDetails["ZEEBE_ADDRESS"] = []byte(inline.ZEEBE_ADDRESS)
	connectionDetails["ZEEBE_AUTHORIZATION_SERVER_URL"] = []byte(inline.ZEEBE_AUTHORIZATION_SERVER_URL)
	cr.Status.AtProvider.ZeebeClientID = inline.ZEEBE_CLIENT_ID
	cr.Status.AtProvider.ZeebeAddress = inline.ZEEBE_ADDRESS
	cr.Status.AtProvider.ZeebeAuthorizationServerUrl = inline.ZEEBE_AUTHORIZATION_SERVER_URL

	return managed.ExternalObservation{
		// Return false when the external resource does not exist. This lets
		// the managed resource reconciler know that it needs to call Create to
		// (re)create the resource, or that it has successfully been deleted.
		ResourceExists: true,

		// Return false when the external resource exists, but it not up to date
		// with the desired managed resource state. This lets the managed
		// resource reconciler know that it needs to call Update.
		ResourceUpToDate: true,

		// Return any details that may be required to connect to the external
		// resource. These will be stored as the connection secret.
		ConnectionDetails: connectionDetails,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	log, _ := logr.FromContext(ctx)
	cr, ok := mg.(*v1alpha1.Client)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotclient)
	}

	clientName := cr.GetName()

	newClientConfiguration := console.CreateClusterClientBody{
		ClientName: clientName,
	}

	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.AccessToken)
	inline, _, err := c.service.APIClient.ClustersApi.CreateClient(ctx, cr.Spec.ForProvider.ClusterID).
		CreateClusterClientBody(newClientConfiguration).
		Execute()
	if err != nil {
		log.Error(err, "client-creation")
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, inline.ClientId)

	cr.Status.AtProvider.ZeebeClientID = inline.ClientId

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{
			"ZEEBE_CLIENT_ID":     []byte(inline.ClientId),
			"ZEEBE_CLIENT_SECRET": []byte(inline.ClientSecret),
		},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	log, _ := logr.FromContext(ctx)
	cr, ok := mg.(*v1alpha1.Client)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotclient)
	}

	log.Info("Updating Client", "client-custom-resource", cr)

	return managed.ExternalUpdate{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) error {
	log, _ := logr.FromContext(ctx)
	cr, ok := mg.(*v1alpha1.Client)
	if !ok {
		return errors.New(errNotclient)
	}

	log.Info("Deleting client", "custom-resource", cr)

	ctx = context.WithValue(ctx, console.ContextAccessToken, c.service.AccessToken)
	resp, err := c.service.ClustersApi.DeleteClient(ctx, cr.Spec.ForProvider.ClusterID, meta.GetExternalName(cr)).
		Execute()
	if err != nil {
		log.Info("the response on client deletion", resp)
		log.Error(err, "the error on client deletion")
		return err
	}

	return nil
}
