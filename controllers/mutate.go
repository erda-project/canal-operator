package controllers

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	v1 "github.com/erda-project/canal-operator/api/v1"
)

const (
	canalContainerName = "canal"
	adminContainerName = "admin"

	canalServicePortName   = "canal"
	adminServicePortName   = "admin"
	metricsServicePortName = "metrics"

	canalAdminScript = "/admin.sh"
	configMapMount   = "/configmaps"

	envKLocal    = "K_LOCAL"
	envKJavaOpts = "K_JAVA_OPTS"
	envKCanalOps = "K_CANAL_OPTS"

	optionCanalAdminManager     = "canal.admin.manager"
	optionCanalAdminPort        = "canal.admin.port"
	optionCanalAdminPassword    = "canal.admin.passwd"
	optionCanalPort             = "canal.port"
	optionCanalMetricsPullPort  = "canal.metrics.pull.port"
	optionCanalServerMode       = "canal.serverMode"
	optionCanalWithoutNetty     = "canal.withoutNetty"
	optionCanalServerModeKafka  = "kafka"
	optionCanalServerModeMQ     = "rocketmq"
	optionCanalServerModeRabbit = "rabbitmq"
	optionCanalServerModePulsar = "pulsarmq"

	defaultCanalAdminPort = 11110
	defaultCanalPort      = 11111
	defaultCanalMetrics   = 11112
	adminProbePort        = 8089

	configMapDefaultMode = 420
)

func MutateSts(canal *v1.Canal, sts *appsv1.StatefulSet) error {
	isAdminManaged := canal.Spec.CanalOptions[optionCanalAdminManager] != ""
	kLocalValue := strconv.FormatBool(isAdminManaged)

	canalProbe, err := newCanalLivenessProbe(canal.Spec.CanalOptions, isAdminManaged)
	if err != nil {
		return err
	}

	labels := canal.NewLabels()
	podLabels := make(map[string]string, len(labels)+len(canal.Spec.Labels))
	for k, v := range canal.Spec.Labels {
		podLabels[k] = v
	}
	for k, v := range labels {
		podLabels[k] = v
	}

	annotations := make(map[string]string, len(canal.Spec.Annotations))
	for k, v := range canal.Spec.Annotations {
		annotations[k] = v
	}

	kCanalOpts := make([]string, 0, len(canal.Spec.CanalOptions))
	for k := range canal.Spec.CanalOptions {
		kCanalOpts = append(kCanalOpts, k)
	}
	sort.Strings(kCanalOpts)
	for i, k := range kCanalOpts {
		v := canal.Spec.CanalOptions[k]
		if k == optionCanalAdminPassword {
			v = v1.EncodePassword(v)
		}
		kCanalOpts[i] = "-D" + k + "=" + v
	}

	spec := canal.Spec.DeepCopy()

	containers := []corev1.Container{
		{
			Name:            canalContainerName,
			Image:           canal.Spec.Image,
			ImagePullPolicy: canal.Spec.ImagePullPolicy,
			Resources:       canal.Spec.Resources,
			EnvFrom:         spec.EnvFrom,
			Env: append(spec.Env, NewEnv(
				corev1.EnvVar{
					Name:  envKLocal,
					Value: kLocalValue,
				},
				corev1.EnvVar{
					Name:  envKJavaOpts,
					Value: canal.Spec.JavaOptions,
				},
				corev1.EnvVar{
					Name:  envKCanalOps,
					Value: strings.Join(kCanalOpts, " "),
				},
			)...),
			LivenessProbe: canalProbe,
			VolumeMounts: []corev1.VolumeMount{
				{
					MountPath: configMapMount,
					Name:      canal.Name,
				},
			},
		},
	}

	if isAdminManaged {
		containers = append(containers, corev1.Container{
			Name:            adminContainerName,
			Image:           canal.Spec.Image,
			ImagePullPolicy: canal.Spec.ImagePullPolicy,
			Command:         []string{canalAdminScript},
			Resources:       canal.Spec.AdminResources,
			EnvFrom:         spec.EnvFrom,
			Env: append(spec.Env, NewEnv(
				corev1.EnvVar{
					Name:  envKLocal,
					Value: kLocalValue,
				},
				corev1.EnvVar{
					Name:  envKJavaOpts,
					Value: canal.Spec.JavaOptions,
				},
			)...),
			LivenessProbe: newTCPLivenessProbe(adminProbePort),
			VolumeMounts: []corev1.VolumeMount{
				{
					MountPath: configMapMount,
					Name:      canal.Name,
				},
			},
		})
	}

	var enableServiceLinks bool

	sts.Spec = appsv1.StatefulSetSpec{
		ServiceName: canal.BuildName(v1.HeadlessSuffix),
		Replicas:    pointer.Int32Ptr(int32(canal.Spec.Replicas)),
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      podLabels,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				EnableServiceLinks: &enableServiceLinks,
				Affinity:           canal.Spec.Affinity.DeepCopy(),
				Containers:         containers,
				Volumes: []corev1.Volume{
					{
						Name: canal.Name,
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								DefaultMode: pointer.Int32(configMapDefaultMode),
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

func newCanalLivenessProbe(options map[string]string, isAdminManaged bool) (*corev1.Probe, error) {
	livenessPort, err := resolveCanalLivenessPort(options, isAdminManaged)
	if err != nil {
		return nil, err
	}
	return newTCPLivenessProbe(livenessPort), nil
}

func resolveCanalLivenessPort(options map[string]string, isAdminManaged bool) (int, error) {
	if isAdminManaged {
		return parsePortOption(options, optionCanalAdminPort, defaultCanalAdminPort)
	}
	if shouldProbeMetricsPort(options) {
		return parsePortOption(options, optionCanalMetricsPullPort, defaultCanalMetrics)
	}
	return parsePortOption(options, optionCanalPort, defaultCanalPort)
}

func shouldProbeMetricsPort(options map[string]string) bool {
	if strings.EqualFold(strings.TrimSpace(options[optionCanalWithoutNetty]), "true") {
		return true
	}

	switch strings.ToLower(strings.TrimSpace(options[optionCanalServerMode])) {
	case optionCanalServerModeKafka, optionCanalServerModeMQ, optionCanalServerModeRabbit, optionCanalServerModePulsar:
		return true
	default:
		return false
	}
}

func parsePortOption(options map[string]string, key string, defaultPort int) (int, error) {
	s := options[key]
	if s == "" {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(s)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("%s invalid", key)
	}
	return port, nil
}

func newTCPLivenessProbe(port int) *corev1.Probe {
	return &corev1.Probe{
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
	}
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

func MutateSvc(canal *v1.Canal, svc *corev1.Service) error {
	adminPort, err := parsePortOption(canal.Spec.CanalOptions, optionCanalAdminPort, defaultCanalAdminPort)
	if err != nil {
		return err
	}
	canalPort, err := parsePortOption(canal.Spec.CanalOptions, optionCanalPort, defaultCanalPort)
	if err != nil {
		return err
	}
	metricsPort, err := parsePortOption(canal.Spec.CanalOptions, optionCanalMetricsPullPort, defaultCanalMetrics)
	if err != nil {
		return err
	}

	svc.Labels = canal.NewLabels()
	svc.Spec = corev1.ServiceSpec{
		Selector: canal.NewLabels(),
		Ports: []corev1.ServicePort{
			{
				Name:       adminServicePortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(adminPort),
				TargetPort: intstr.FromInt(adminPort),
			},
			{
				Name:       canalServicePortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(canalPort),
				TargetPort: intstr.FromInt(canalPort),
			},
			{
				Name:       metricsServicePortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(metricsPort),
				TargetPort: intstr.FromInt(metricsPort),
			},
		},
	}

	return nil
}

func MutateSvcAdmin(canal *v1.Canal, svc *corev1.Service) error {
	svc.Labels = canal.NewLabels()
	svc.Spec = corev1.ServiceSpec{
		Selector: canal.NewLabels(),
		Ports: []corev1.ServicePort{
			{
				Name:       adminServicePortName,
				Protocol:   corev1.ProtocolTCP,
				Port:       int32(adminProbePort),
				TargetPort: intstr.FromInt(adminProbePort),
			},
		},
	}

	return nil
}
