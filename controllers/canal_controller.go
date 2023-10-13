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
	"os"
	"os/exec"
	"strings"

	v1 "github.com/erda-project/canal-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
	canal := &v1.Canal{}
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
	if err := canal.Validate(); err != nil {
		log.Error(err, "Validate failed")
		return zeroResult, err
	}

	if err := r.Update(ctx, canal); err != nil {
		return zeroResult, err
	}

	kLocal := canal.Spec.CanalOptions["canal.admin.manager"] != ""
	if kLocal && !canal.Status.AdminInitialized {
		{ //cm
			applicationYml, ok := cm.Data["application.yml"]
			if !ok {
				applicationYml = ApplicationYml
			}
			applicationYml, _ = sedLine(applicationYml, "address: ", canal.Spec.AdminOptions["spring.datasource.address"])
			applicationYml, _ = sedLine(applicationYml, "database: ", canal.Spec.AdminOptions["spring.datasource.database"])
			applicationYml, _ = sedLine(applicationYml, "username: ", canal.Spec.AdminOptions["spring.datasource.username"])
			applicationYml, _ = sedLine(applicationYml, "password: ", "'"+canal.Spec.AdminOptions["spring.datasource.password"]+"'")
			applicationYml, _ = sedLine(applicationYml, "adminUser: ", canal.Spec.AdminOptions["canal.adminUser"])
			applicationYml, _ = sedLine(applicationYml, "adminPasswd: ", "'"+canal.Spec.AdminOptions["canal.adminPasswd"]+"'")
			if cm.Data == nil {
				cm.Data = make(map[string]string)
			}
			cm.Data["application.yml"] = applicationYml
			err := r.Update(ctx, cm)
			if err != nil {
				log.Error(err, "Update ConfigMap failed")
				return zeroResult, err
			}
		}

		{ //db
			b, err := os.ReadFile("/canal_manager.sql") // TODO Version
			if err != nil {
				log.Error(err, "Read canal_manager.sql failed")
				return zeroResult, err
			}
			s := string(b)
			s = strings.ReplaceAll(s, "`canal_manager`", "`"+canal.Spec.AdminOptions["spring.datasource.database"]+"`")
			s = strings.ReplaceAll(s, "DROP TABLE IF EXISTS `", "-- DROP TABLE IF EXISTS `") //不丢数据，只能手动清理重新初始化
			// s = strings.ReplaceAll(s, "CREATE TABLE `", "CREATE TABLE IF NOT EXISTS `")
			// s = strings.ReplaceAll(s, "'admin', '6BB4837EB74329105EE4568DDA7DC67ED2CA2AD9'",
			// 	"'"+canal.Spec.AdminOptions["canal.adminUser"]+"', '"+v1.EncodePassword(canal.Spec.AdminOptions["canal.adminPasswd"])+"'")

			h := canal.Spec.AdminOptions["spring.datasource.address"]
			P := "3306"
			if i := strings.LastIndexByte(h, ':'); i != -1 {
				P = h[i+1:]
				h = h[:i]
			}
			cmd := exec.Command("mysql",
				"-h"+h,
				"-P"+P,
				"-u"+canal.Spec.AdminOptions["spring.datasource.username"],
				"-p"+canal.Spec.AdminOptions["spring.datasource.password"],
			)
			cmd.Stdin = strings.NewReader(s)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
			if err := cmd.Run(); err != nil {
				log.Error(err, "Import canal_manager.sql failed")
				return zeroResult, err
			}
		}

		canal.Status.AdminInitialized = true
		if err := r.Status().Update(ctx, canal); err != nil {
			return zeroResult, err
		}
	}

	headlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canal.BuildName(v1.HeadlessSuffix),
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

	adminSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      canal.BuildName("admin"),
			Namespace: canal.Namespace,
		},
	}
	if kLocal {
		opResult, err = ctrl.CreateOrUpdate(ctx, r.Client, adminSvc, func() error {
			MutateSvcAdmin(canal, adminSvc)
			return ctrl.SetControllerReference(canal, adminSvc, r.Scheme)
		})
		if err != nil {
			log.Error(err, "CreateOrUpdate admin svc failed")
			return ctrl.Result{}, err
		}
		log.Info("CreateOrUpdate admin svc succeeded", "OperationResult", opResult)
	} else {
		err = r.Delete(ctx, adminSvc)
		if err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Delete admin svc failed")
			return ctrl.Result{}, err
		}
	}

	var color string
	if sts.Status.Replicas > 0 {
		if sts.Status.Replicas == sts.Status.ReadyReplicas &&
			sts.Status.UpdateRevision == sts.Status.CurrentRevision &&
			sts.Status.Replicas == sts.Status.UpdatedReplicas &&
			sts.Status.Replicas == sts.Status.CurrentReplicas {
			color = v1.Green
		} else if sts.Status.ReadyReplicas > 0 {
			color = v1.Yellow
		} else {
			color = v1.Red
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
		For(&v1.Canal{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

func sedLine(s, k, v string) (string, bool) {
	i := strings.Index(s, k)
	if i == -1 {
		return s, false
	}
	t := s[i:]
	j := strings.IndexByte(t, '\n')
	if j == -1 {
		j = len(t)
	}
	return s[:i] + k + v + t[j:], true
}

const ApplicationYml = `server:
  port: 8089
spring:
  jackson:
    date-format: yyyy-MM-dd HH:mm:ss
    time-zone: GMT+8

spring.datasource:
  address: 127.0.0.1:3306
  database: canal_manager
  username: canal
  password: canal
  driver-class-name: com.mysql.jdbc.Driver
  url: jdbc:mysql://${spring.datasource.address}/${spring.datasource.database}?useUnicode=true&characterEncoding=UTF-8&useSSL=false
  hikari:
    maximum-pool-size: 30
    minimum-idle: 1

canal:
  adminUser: admin
  adminPasswd: admin`
