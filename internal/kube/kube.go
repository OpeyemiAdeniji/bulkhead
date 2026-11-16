// Package kube connects to a cluster with the user's kubeconfig, and asks the API server
// who a request is made as.
package kube

import (
	"context"
	"fmt"
	"slices"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Options selects the kubeconfig and the context. Empty values follow kubectl's rules:
// $KUBECONFIG, then ~/.kube/config, and the current context.
type Options struct {
	Kubeconfig string
	Context    string
}

// Cluster is a connection to one cluster, as one identity.
type Cluster struct {
	Context string
	Server  string
	Config  *rest.Config
	Client  kubernetes.Interface
	// As is the identity requests are impersonated as, or nil for the user's own identity.
	As *Identity
}

// Identity is who requests are made as: a user name and groups.
type Identity struct {
	User   string   `json:"user"`
	Groups []string `json:"groups,omitempty"`
}

// String returns the identity in the form used in messages.
func (i Identity) String() string {
	if len(i.Groups) == 0 {
		return i.User
	}
	return fmt.Sprintf("%s (groups %s)", i.User, strings.Join(i.Groups, ", "))
}

// Connect builds a connection from the kubeconfig.
func Connect(o Options) (*Cluster, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if o.Kubeconfig != "" {
		rules.ExplicitPath = o.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: o.Context}
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	raw, err := loader.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("read the kubeconfig: %w", err)
	}
	ctxName := o.Context
	if ctxName == "" {
		ctxName = raw.CurrentContext
	}
	if ctxName == "" {
		return nil, fmt.Errorf("the kubeconfig has no current context; pass --context")
	}
	if _, ok := raw.Contexts[ctxName]; !ok {
		return nil, fmt.Errorf("context %q is not in the kubeconfig", ctxName)
	}
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build the client configuration for context %q: %w", ctxName, err)
	}
	cfg.UserAgent = "bulkhead-check"
	// Deprecation warnings (for example for Endpoints, which GRD-07 writes on purpose) would mix
	// with the report. The checks report what matters themselves.
	cfg.WarningHandler = rest.NoWarnings{}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Cluster{Context: ctxName, Server: cfg.Host, Config: cfg, Client: client}, nil
}

// Impersonate returns a connection that makes every request as the given identity.
// The user's own identity needs the right to impersonate users and groups.
func (c *Cluster) Impersonate(id Identity) (*Cluster, error) {
	cfg := rest.CopyConfig(c.Config)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: id.User, Groups: slices.Clone(id.Groups)}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Cluster{Context: c.Context, Server: c.Server, Config: cfg, Client: client, As: &id}, nil
}

// ServerVersion returns the version of the API server, for example v1.37.0.
func (c *Cluster) ServerVersion() (string, error) {
	v, err := c.Client.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return v.GitVersion, nil
}

// WhoAmI asks the API server who requests of this connection are made as (SelfSubjectReview).
func (c *Cluster) WhoAmI(ctx context.Context) (Identity, error) {
	return WhoAmI(ctx, c.Client)
}

// WhoAmI asks the API server who requests of this client are made as (SelfSubjectReview).
func WhoAmI(ctx context.Context, client kubernetes.Interface) (Identity, error) {
	r, err := client.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return Identity{}, err
	}
	u := r.Status.UserInfo
	return Identity{User: u.Username, Groups: u.Groups}, nil
}

// Confirm checks that the identity the API server reports is the expected one: the same
// user name, and every expected group. Kubernetes adds groups such as system:authenticated,
// so extra groups are fine.
func Confirm(expected, got Identity) error {
	if got.User != expected.User {
		return fmt.Errorf("the API server reports user %q, expected %q", got.User, expected.User)
	}
	for _, g := range expected.Groups {
		if !slices.Contains(got.Groups, g) {
			return fmt.Errorf("the API server reports the groups %v, which lack %q", got.Groups, g)
		}
	}
	return nil
}
