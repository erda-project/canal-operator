package controllers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	databasev1 "github.com/erda-project/canal-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

func MutateSts(canal *databasev1.Canal, sts *appsv1.StatefulSet) error {
	var err error
	var port int

	if s := canal.Spec.CanalOptions["canal.port"]; s == "" {
		port = 11111
	} else {
		port, err = strconv.Atoi(s)
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("canal.port invalid")
		}
	}

	labels := canal.NewLabels()
	podLables := make(map[string]string, len(labels)+len(canal.Spec.Labels))
	for k, v := range canal.Spec.Labels {
		podLables[k] = v
	}
	for k, v := range labels {
		podLables[k] = v
	}

	annotations := make(map[string]string, len(canal.Spec.Annotations))
	for k, v := range canal.Spec.Annotations {
		annotations[k] = v
	}

	kLocal := "false"
	if canal.Spec.CanalOptions["canal.admin.manager"] != "" {
		kLocal = "true"
	}

	kCanalOpts := make([]string, 0, len(canal.Spec.CanalOptions))
	for k := range canal.Spec.CanalOptions {
		kCanalOpts = append(kCanalOpts, k)
	}
	sort.Strings(kCanalOpts)
	for i, k := range kCanalOpts {
		kCanalOpts[i] = "-D" + k + "=" + canal.Spec.CanalOptions[k]
	}

	spec := canal.Spec.DeepCopy()

	sts.Spec = appsv1.StatefulSetSpec{
		ServiceName: canal.BuildName(databasev1.HeadlessSuffix),
		Replicas:    pointer.Int32Ptr(int32(canal.Spec.Replicas)),
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      podLables,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				Affinity: canal.Spec.Affinity.DeepCopy(),
				Containers: []corev1.Container{
					{
						Name:            "canal",
						Image:           canal.Spec.Image,
						ImagePullPolicy: canal.Spec.ImagePullPolicy,
						Resources:       canal.Spec.Resources,
						EnvFrom:         spec.EnvFrom,
						Env: append(spec.Env, NewEnv(
							corev1.EnvVar{
								Name:  "K_LOCAL",
								Value: kLocal,
							},
							corev1.EnvVar{
								Name:  "K_JAVA_OPTS",
								Value: canal.Spec.JavaOptions,
							},
							corev1.EnvVar{
								Name:  "K_CANAL_OPTS",
								Value: strings.Join(kCanalOpts, " "),
							},
						)...),
						LivenessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{
									Port: intstr.FromInt(port),
								},
							},
							// 1m
							FailureThreshold:    12,
							InitialDelaySeconds: 1,
							PeriodSeconds:       5,
							SuccessThreshold:    1,
							TimeoutSeconds:      1,
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								MountPath: "/configmaps",
								Name:      canal.Name,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: canal.Name,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								DefaultMode: pointer.Int32(420),
								LocalObjectReference: corev1.LocalObjectReference{
									Name: canal.Name,
								},
							},
						},
					},
				},
			},
		},
	}

	return nil
}

func NewEnv(a ...corev1.EnvVar) []corev1.EnvVar {
	return append([]corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{
			Name: "NODE_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "spec.nodeName",
				},
			},
		},
		{
			Name: "HOST_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.hostIP",
				},
			},
		},
		{
			Name: "POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
	}, a...)
}

func MutateSvc(canal *databasev1.Canal, svc *corev1.Service) error {
	var err error
	var adminPort, port, metricsPort int

	if s := canal.Spec.CanalOptions["canal.admin.port"]; s == "" {
		adminPort = 11110
	} else {
		adminPort, err = strconv.Atoi(s)
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("canal.admin.port invalid")
		}
	}

	if s := canal.Spec.CanalOptions["canal.port"]; s == "" {
		port = 11111
	} else {
		port, err = strconv.Atoi(s)
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("canal.port invalid")
		}
	}

	if s := canal.Spec.CanalOptions["canal.metrics.pull.port"]; s == "" {
		metricsPort = 11112
	} else {
		metricsPort, err = strconv.Atoi(s)
		if err != nil || port <= 0 || port > 65535 {
			return fmt.Errorf("canal.metrics.pull.port invalid")
		}
	}

	svc.Labels = canal.NewLabels()
	svc.Spec = corev1.ServiceSpec{
		ClusterIP: corev1.ClusterIPNone,
		Selector:  canal.NewLabels(),
		Ports: []corev1.ServicePort{
			{
				Name:       "admin",
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(adminPort),
				TargetPort: intstr.FromInt(adminPort),
			},
			{
				Name:       "canal",
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(port),
				TargetPort: intstr.FromInt(port),
			},
			{
				Name:       "metrics",
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(metricsPort),
				TargetPort: intstr.FromInt(metricsPort),
			},
		},
	}

	return nil
}
