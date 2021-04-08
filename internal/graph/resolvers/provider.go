package resolvers

import (
	"context"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"

	pkgv1 "github.com/crossplane/crossplane/apis/pkg/v1"

	"github.com/upbound/xgql/internal/auth"
	"github.com/upbound/xgql/internal/graph/model"
)

const (
	errListProviderRevs = "cannot list provider revisions"
	errGetCRD           = "cannot get custom resource definition"
)

type provider struct {
	clients ClientCache
}

func (r *provider) Events(ctx context.Context, obj *model.Provider) (*model.EventConnection, error) {
	e := &events{clients: r.clients}
	return e.Resolve(ctx, &corev1.ObjectReference{
		APIVersion: obj.APIVersion,
		Kind:       obj.Kind,
		Name:       obj.Metadata.Name,
		UID:        types.UID(obj.Metadata.UID),
	})
}

func (r *provider) Revisions(ctx context.Context, obj *model.Provider, active *bool) (*model.ProviderRevisionConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	creds, _ := auth.FromContext(ctx)
	c, err := r.clients.Get(creds)
	if err != nil {
		graphql.AddError(ctx, errors.Wrap(err, errGetClient))
		return nil, nil
	}

	in := &pkgv1.ProviderRevisionList{}
	if err := c.List(ctx, in); err != nil {
		graphql.AddError(ctx, errors.Wrap(err, errListProviderRevs))
		return nil, nil
	}

	out := &model.ProviderRevisionConnection{
		Nodes: make([]model.ProviderRevision, 0),
	}

	for i := range in.Items {
		pr := in.Items[i] // So we don't take the address of a range variable.

		// We're not the controller reference of this ProviderRevision; it's not
		// one of ours.
		// https://github.com/kubernetes/community/blob/0331e/contributors/design-proposals/api-machinery/controller-ref.md
		if c := metav1.GetControllerOf(&pr); c == nil || c.UID != types.UID(obj.Metadata.UID) {
			continue
		}

		// We only want the active PackageRevision, and this isn't it.
		if pointer.BoolPtrDerefOr(active, false) && pr.Spec.DesiredState != pkgv1.PackageRevisionActive {
			continue
		}

		out.Nodes = append(out.Nodes, model.GetProviderRevision(&pr))
		out.TotalCount++
	}

	return out, nil
}

type providerRevision struct {
	clients ClientCache
}

func (r *providerRevision) Events(ctx context.Context, obj *model.ProviderRevision) (*model.EventConnection, error) {
	e := &events{clients: r.clients}
	return e.Resolve(ctx, &corev1.ObjectReference{
		APIVersion: obj.APIVersion,
		Kind:       obj.Kind,
		Name:       obj.Metadata.Name,
		UID:        types.UID(obj.Metadata.UID),
	})
}

type providerRevisionStatus struct {
	clients ClientCache
}

func (r *providerRevisionStatus) Objects(ctx context.Context, obj *model.ProviderRevisionStatus) (*model.KubernetesResourceConnection, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	creds, _ := auth.FromContext(ctx)
	c, err := r.clients.Get(creds)
	if err != nil {
		graphql.AddError(ctx, errors.Wrap(err, errGetClient))
		return nil, nil
	}

	out := &model.KubernetesResourceConnection{
		Nodes: make([]model.KubernetesResource, 0, len(obj.ObjectRefs)),
	}

	for _, ref := range obj.ObjectRefs {
		// Crossplane lints provider packages to ensure they only contain CRDs,
		// but this isn't enforced at the API level. We filter out anything that
		// isn't a CRD, just in case.
		if ref.Kind != "CustomResourceDefinition" {
			continue
		}
		if strings.Split(ref.APIVersion, "/")[0] != kextv1.GroupName {
			continue
		}

		crd := &kextv1.CustomResourceDefinition{}
		if err := c.Get(ctx, types.NamespacedName{Name: ref.Name}, crd); err != nil {
			graphql.AddError(ctx, errors.Wrap(err, errGetCRD))
			continue
		}

		out.Nodes = append(out.Nodes, model.GetCustomResourceDefinition(crd))
		out.TotalCount++
	}

	return out, nil
}
