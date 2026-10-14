package ask

import "strings"

var K8sStackItems = []string{
	"rancher   — Rancher-managed clusters (kubeconfig from Rancher)",
	"openshift — Red Hat OpenShift (oc client)",
	"gke       — Google Kubernetes Engine (GCP)",
	"eks       — Amazon Elastic Kubernetes Service",
	"aks       — Azure Kubernetes Service",
	"plain     — plain kubeconfig / anything else",
}

var CloudItems = []string{
	"aws    — Amazon Web Services",
	"gcp    — Google Cloud",
	"azure  — Microsoft Azure",
	"none   — on-prem / other / skip cloud tooling",
}

func TokensFromSelection(selected []string) string {
	tokens := make([]string, 0, len(selected))
	for _, line := range selected {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		tokens = append(tokens, fields[0])
	}
	return strings.Join(tokens, " ")
}
