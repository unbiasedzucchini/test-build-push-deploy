package deploy

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// Config holds the parameters for a deployment.
type Config struct {
	Name      string
	Namespace string
	Image     string
	Port      int32
	Replicas  int32
}

// Apply creates a Deployment and Service for the given config.
func Apply(ctx context.Context, client kubernetes.Interface, cfg Config) error {
	labels := map[string]string{"app": cfg.Name}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(cfg.Replicas),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  cfg.Name,
						Image: cfg.Image,
						Ports: []corev1.ContainerPort{{
							ContainerPort: cfg.Port,
						}},
					}},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.Name,
			Namespace: cfg.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Port:       cfg.Port,
				TargetPort: intstr.FromInt32(cfg.Port),
			}},
		},
	}

	if _, err := client.AppsV1().Deployments(cfg.Namespace).Create(ctx, dep, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating deployment: %w", err)
	}
	if _, err := client.CoreV1().Services(cfg.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	return nil
}
