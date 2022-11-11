/*
Copyright 2022.

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

package controllers

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	databasev1 "github.com/erda-project/canal-operator/api/v1"
)

// CanalReconciler reconciles a Canal object
type CanalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=database.erda.cloud,resources=canals,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=database.erda.cloud,resources=canals/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=database.erda.cloud,resources=canals/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Canal object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.2/pkg/reconcile
func (r *CanalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	canal := &databasev1.Canal{}
	zeroResult := ctrl.Result{}

	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, req.NamespacedName, cm); err != nil {
		if apierrors.IsNotFound(err) {
			cm.Name = req.Name
			cm.Namespace = req.Namespace
			err = r.Create(ctx, cm)
		}
		if err != nil {
			log.Error(err, "GetOrCreate ConfigMap failed")
			return zeroResult, err
		}
	}

	if err := r.Get(ctx, req.NamespacedName, canal); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to fetch Canal")
		}
		return zeroResult, err
	}

	canal.Default()
	if err := r.Update(ctx, canal); err != nil {
		return zeroResult, err
	}

	headlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canal.BuildName(databasev1.HeadlessSuffix),
			Namespace: canal.Namespace,
		},
	}
	opResult, err := ctrl.CreateOrUpdate(ctx, r.Client, headlessSvc, func() error {
		err := MutateSvc(canal, headlessSvc)
		if err != nil {
			return err
		}
		return ctrl.SetControllerReference(canal, headlessSvc, r.Scheme)
	})
	if err != nil {
		log.Error(err, "CreateOrUpdate headless svc failed")
		return ctrl.Result{}, err
	}
	log.Info("CreateOrUpdate headless svc succeeded", "OperationResult", opResult)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canal.Name,
			Namespace: canal.Namespace,
		},
	}
	opResult, err = ctrl.CreateOrUpdate(ctx, r.Client, sts, func() error {
		err := MutateSts(canal, sts)
		if err != nil {
			return err
		}
		return ctrl.SetControllerReference(canal, sts, r.Scheme)
	})
	if err != nil {
		log.Error(err, "CreateOrUpdate sts failed")
		return ctrl.Result{}, err
	}
	log.Info("CreateOrUpdate sts succeeded", "OperationResult", opResult)

	var color string
	if sts.Status.Replicas > 0 {
		if sts.Status.Replicas == sts.Status.ReadyReplicas &&
			sts.Status.UpdateRevision == sts.Status.CurrentRevision &&
			sts.Status.Replicas == sts.Status.UpdatedReplicas &&
			sts.Status.Replicas == sts.Status.CurrentReplicas {
			color = databasev1.Green
		} else if sts.Status.ReadyReplicas > 0 {
			color = databasev1.Yellow
		} else {
			color = databasev1.Red
		}
	}
	if color != canal.Status.Color {
		canal.Status.Color = color
		if err := r.Status().Update(ctx, canal); err != nil {
			return zeroResult, err
		}
	}

	return zeroResult, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CanalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1.Canal{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
