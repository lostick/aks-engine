// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT license.

package api

import (
	"strconv"
	"strings"

	"github.com/Azure/go-autorest/autorest/to"

	"github.com/Azure/aks-engine/pkg/api/common"
)

func (cs *ContainerService) setKubeletConfig(isUpgrade bool) {
	o := cs.Properties.OrchestratorProfile
	staticLinuxKubeletConfig := map[string]string{
		"--address":                     "0.0.0.0",
		"--allow-privileged":            "true",
		"--anonymous-auth":              "false",
		"--authorization-mode":          "Webhook",
		"--client-ca-file":              "/etc/kubernetes/certs/ca.crt",
		"--pod-manifest-path":           "/etc/kubernetes/manifests",
		"--cluster-dns":                 o.KubernetesConfig.DNSServiceIP,
		"--cgroups-per-qos":             "true",
		"--kubeconfig":                  "/var/lib/kubelet/kubeconfig",
		"--keep-terminated-pod-volumes": "false",
		"--tls-cert-file":               "/etc/kubernetes/certs/kubeletserver.crt",
		"--tls-private-key-file":        "/etc/kubernetes/certs/kubeletserver.key",
	}

	// Start with copy of Linux config
	staticWindowsKubeletConfig := make(map[string]string)
	for key, val := range staticLinuxKubeletConfig {
		switch key {
		case "--pod-manifest-path", "--tls-cert-file", "--tls-private-key-file": // Don't add Linux-specific config
			staticWindowsKubeletConfig[key] = ""
		case "--anonymous-auth", "--client-ca-file":
			if !to.Bool(o.KubernetesConfig.EnableSecureKubelet) { // Don't add if EnableSecureKubelet is disabled
				staticWindowsKubeletConfig[key] = ""
			} else {
				staticWindowsKubeletConfig[key] = val
			}
		default:
			staticWindowsKubeletConfig[key] = val
		}
	}

	// Add Windows-specific overrides
	// Eventually paths should not be hardcoded here. They should be relative to $global:KubeDir in the PowerShell script
	staticWindowsKubeletConfig["--azure-container-registry-config"] = "c:\\k\\azure.json"
	staticWindowsKubeletConfig["--pod-infra-container-image"] = "kubletwin/pause"
	staticWindowsKubeletConfig["--kubeconfig"] = "c:\\k\\config"
	staticWindowsKubeletConfig["--cloud-config"] = "c:\\k\\azure.json"
	staticWindowsKubeletConfig["--cgroups-per-qos"] = "false"
	staticWindowsKubeletConfig["--enforce-node-allocatable"] = "\"\"\"\""
	staticWindowsKubeletConfig["--system-reserved"] = "memory=2Gi"
	staticWindowsKubeletConfig["--client-ca-file"] = "c:\\k\\ca.crt"
	staticWindowsKubeletConfig["--hairpin-mode"] = "promiscuous-bridge"
	staticWindowsKubeletConfig["--image-pull-progress-deadline"] = "20m"
	staticWindowsKubeletConfig["--resolv-conf"] = "\"\"\"\""
	staticWindowsKubeletConfig["--eviction-hard"] = "\"\"\"\""

	// Default Kubelet config
	defaultKubeletConfig := map[string]string{
		"--cluster-domain":                    "cluster.local",
		"--network-plugin":                    "cni",
		"--pod-infra-container-image":         o.KubernetesConfig.KubernetesImageBase + K8sComponentsByVersionMap[o.OrchestratorVersion]["pause"],
		"--max-pods":                          strconv.Itoa(DefaultKubernetesMaxPods),
		"--eviction-hard":                     DefaultKubernetesHardEvictionThreshold,
		"--node-status-update-frequency":      K8sComponentsByVersionMap[o.OrchestratorVersion]["nodestatusfreq"],
		"--image-gc-high-threshold":           strconv.Itoa(DefaultKubernetesGCHighThreshold),
		"--image-gc-low-threshold":            strconv.Itoa(DefaultKubernetesGCLowThreshold),
		"--non-masquerade-cidr":               DefaultNonMasqueradeCIDR,
		"--cloud-provider":                    "azure",
		"--cloud-config":                      "/etc/kubernetes/azure.json",
		"--azure-container-registry-config":   "/etc/kubernetes/azure.json",
		"--event-qps":                         DefaultKubeletEventQPS,
		"--cadvisor-port":                     DefaultKubeletCadvisorPort,
		"--pod-max-pids":                      strconv.Itoa(DefaultKubeletPodMaxPIDs),
		"--image-pull-progress-deadline":      "30m",
		"--enforce-node-allocatable":          "pods",
		"--streaming-connection-idle-timeout": "5m",
	}

	// Set --non-masquerade-cidr if ip-masq-agent is disabled on AKS
	if !cs.Properties.IsIPMasqAgentEnabled() {
		defaultKubeletConfig["--non-masquerade-cidr"] = cs.Properties.OrchestratorProfile.KubernetesConfig.ClusterSubnet
	}

	// Apply Azure CNI-specific --max-pods value
	if o.KubernetesConfig.NetworkPlugin == NetworkPluginAzure {
		defaultKubeletConfig["--max-pods"] = strconv.Itoa(DefaultKubernetesMaxPodsVNETIntegrated)
	}

	minVersionRotateCerts := "1.11.9"
	if common.IsKubernetesVersionGe(o.OrchestratorVersion, minVersionRotateCerts) {
		defaultKubeletConfig["--rotate-certificates"] = "true"
	}

	// Disable Weak TLS Cipher Suites for 1.10 and above
	if common.IsKubernetesVersionGe(o.OrchestratorVersion, "1.10.0") {
		defaultKubeletConfig["--tls-cipher-suites"] = TLSStrongCipherSuitesKubelet
	}

	// If no user-configurable kubelet config values exists, use the defaults
	setMissingKubeletValues(o.KubernetesConfig, defaultKubeletConfig)
	addDefaultFeatureGates(o.KubernetesConfig.KubeletConfig, o.OrchestratorVersion, "1.8.0", "PodPriority=true")
	addDefaultFeatureGates(o.KubernetesConfig.KubeletConfig, o.OrchestratorVersion, minVersionRotateCerts, "RotateKubeletServerCertificate=true")

	// Override default cloud-provider?
	if to.Bool(o.KubernetesConfig.UseCloudControllerManager) {
		staticLinuxKubeletConfig["--cloud-provider"] = "external"
	}

	// Override default --network-plugin?
	if o.KubernetesConfig.NetworkPlugin == NetworkPluginKubenet {
		if o.KubernetesConfig.NetworkPolicy != NetworkPolicyCalico {
			o.KubernetesConfig.KubeletConfig["--network-plugin"] = NetworkPluginKubenet
		}
	}

	// We don't support user-configurable values for the following,
	// so any of the value assignments below will override user-provided values
	for key, val := range staticLinuxKubeletConfig {
		o.KubernetesConfig.KubeletConfig[key] = val
	}

	// Remove secure kubelet flags, if configured
	if !to.Bool(o.KubernetesConfig.EnableSecureKubelet) {
		for _, key := range []string{"--anonymous-auth", "--client-ca-file"} {
			delete(o.KubernetesConfig.KubeletConfig, key)
		}
	}

	if isUpgrade && common.IsKubernetesVersionGe(o.OrchestratorVersion, "1.14.0") {
		hasSupportPodPidsLimitFeatureGate := strings.Contains(o.KubernetesConfig.KubeletConfig["--feature-gates"], "SupportPodPidsLimit=true")
		podMaxPids, _ := strconv.Atoi(o.KubernetesConfig.KubeletConfig["--pod-max-pids"])
		if podMaxPids > 0 {
			// If we don't have an explicit SupportPodPidsLimit=true, disable --pod-max-pids by setting to -1
			// To prevent older clusters from inheriting SupportPodPidsLimit=true implicitly starting w/ 1.14.0
			if !hasSupportPodPidsLimitFeatureGate {
				o.KubernetesConfig.KubeletConfig["--pod-max-pids"] = strconv.Itoa(-1)
			}
		}
	}

	removeKubeletFlags(o.KubernetesConfig.KubeletConfig, o.OrchestratorVersion)

	// Master-specific kubelet config changes go here
	if cs.Properties.MasterProfile != nil {
		if cs.Properties.MasterProfile.KubernetesConfig == nil {
			cs.Properties.MasterProfile.KubernetesConfig = &KubernetesConfig{}
			cs.Properties.MasterProfile.KubernetesConfig.KubeletConfig = make(map[string]string)
		}
		setMissingKubeletValues(cs.Properties.MasterProfile.KubernetesConfig, o.KubernetesConfig.KubeletConfig)
		addDefaultFeatureGates(cs.Properties.MasterProfile.KubernetesConfig.KubeletConfig, o.OrchestratorVersion, "", "")

		removeKubeletFlags(cs.Properties.MasterProfile.KubernetesConfig.KubeletConfig, o.OrchestratorVersion)
	}

	// Agent-specific kubelet config changes go here
	for _, profile := range cs.Properties.AgentPoolProfiles {
		if profile.KubernetesConfig == nil {
			profile.KubernetesConfig = &KubernetesConfig{}
			profile.KubernetesConfig.KubeletConfig = make(map[string]string)
		}

		if profile.OSType == Windows {
			for key, val := range staticWindowsKubeletConfig {
				profile.KubernetesConfig.KubeletConfig[key] = val
			}
		} else {
			for key, val := range staticLinuxKubeletConfig {
				profile.KubernetesConfig.KubeletConfig[key] = val
			}
		}

		setMissingKubeletValues(profile.KubernetesConfig, o.KubernetesConfig.KubeletConfig)

		// For N Series (GPU) VMs
		if strings.Contains(profile.VMSize, "Standard_N") {
			if !cs.Properties.IsNVIDIADevicePluginEnabled() && !common.IsKubernetesVersionGe(o.OrchestratorVersion, "1.11.0") {
				// enabling accelerators for Kubernetes >= 1.6 to <= 1.9
				addDefaultFeatureGates(profile.KubernetesConfig.KubeletConfig, o.OrchestratorVersion, "1.6.0", "Accelerators=true")
			}
		}

		removeKubeletFlags(profile.KubernetesConfig.KubeletConfig, o.OrchestratorVersion)
	}
}

func removeKubeletFlags(k map[string]string, v string) {
	// Get rid of values not supported until v1.10
	if !common.IsKubernetesVersionGe(v, "1.10.0") {
		for _, key := range []string{"--pod-max-pids"} {
			delete(k, key)
		}
	}

	// Get rid of values not supported in v1.12 and up
	if common.IsKubernetesVersionGe(v, "1.12.0") {
		for _, key := range []string{"--cadvisor-port"} {
			delete(k, key)
		}
	}

	// Get rid of values not supported in v1.15 and up
	if common.IsKubernetesVersionGe(v, "1.15.0-beta.1") {
		for _, key := range []string{"--allow-privileged"} {
			delete(k, key)
		}
	}

	// Get rid of keys with empty string values
	for key, val := range k {
		if val == "" {
			delete(k, key)
		}
	}
}

func setMissingKubeletValues(p *KubernetesConfig, d map[string]string) {
	if p.KubeletConfig == nil {
		p.KubeletConfig = d
	} else {
		for key, val := range d {
			// If we don't have a user-configurable value for each option
			if _, ok := p.KubeletConfig[key]; !ok {
				// then assign the default value
				p.KubeletConfig[key] = val
			}
		}
	}
}
