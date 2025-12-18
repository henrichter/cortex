package weighers

import (
	"log/slog"
	"testing"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPowerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    PowerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			opts: PowerConfig{
				MinIdleWatts: 100,
				MaxUtilWatts: 200,
			},
			wantErr: false,
		},
		{
			name: "negative minIdleWatts",
			opts: PowerConfig{
				MinIdleWatts: -10,
				MaxUtilWatts: 200,
			},
			wantErr: true,
		},
		{
			name: "negative maxUtilWatts",
			opts: PowerConfig{
				MinIdleWatts: 100,
				MaxUtilWatts: -50,
			},
			wantErr: true,
		},
		{
			name: "maxUtilWatts less than minIdleWatts",
			opts: PowerConfig{
				MinIdleWatts: 200,
				MaxUtilWatts: 100,
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate("hosts[DRS-001]")
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestPowerConfig_powerPerVCPU(t *testing.T) {
	tests := []struct {
		cfg      PowerConfig
		vcpus    int
		expected float64
	}{
		{
			cfg: PowerConfig{
				MinIdleWatts: 100,
				MaxUtilWatts: 200,
			},
			vcpus:    4,
			expected: 25.0,
		},
		{
			cfg: PowerConfig{
				MinIdleWatts: 150,
				MaxUtilWatts: 350,
			},
			vcpus:    5,
			expected: 40.0,
		},
	}

	for _, tc := range tests {
		result := tc.cfg.wattsPerVCPU(float64(tc.vcpus))
		if result != tc.expected {
			t.Errorf("wattsPerVCPU(%d) = %v; want %v", tc.vcpus, result, tc.expected)
		}
	}
}

func TestVMWareEnergyEfficiencyStepOpts_Validate(t *testing.T) {
	tests := []struct {
		name    string
		opts    VMWareEnergyEfficiencyStepOpts
		wantErr bool
	}{
		{
			name: "valid config",
			opts: VMWareEnergyEfficiencyStepOpts{
				HostPowerConfig: map[string]PowerConfig{
					"host1": {MinIdleWatts: 100, MaxUtilWatts: 200},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid config",
			opts: VMWareEnergyEfficiencyStepOpts{
				HostPowerConfig: map[string]PowerConfig{
					"host1": {MinIdleWatts: -100, MaxUtilWatts: 200},
				},
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestVMWareEnergyEfficiencyStep_Run(t *testing.T) {
	scheme, err := v1alpha1.SchemeBuilder.Build()
	if err != nil {
		t.Fatalf("failed to build scheme: %v", err)
	}

	tests := []struct {
		name           string
		stepOpts       VMWareEnergyEfficiencyStepOpts
		knowledge      []shared.HostUtilization
		reqHosts       []api.ExternalSchedulerHost
		resActivations map[string]float64
	}{
		{
			name: "host without power config should be skipped",
			stepOpts: VMWareEnergyEfficiencyStepOpts{
				HostPowerConfig: map[string]PowerConfig{
					"host1": {MinIdleWatts: 180, MaxUtilWatts: 320},
				},
			},
			knowledge: []shared.HostUtilization{
				{ComputeHost: "host1", TotalVCPUsAllocatable: 128.0},
				{ComputeHost: "host2", TotalVCPUsAllocatable: 256.0},
			},
			reqHosts: []api.ExternalSchedulerHost{
				{ComputeHost: "host1"},
				{ComputeHost: "host2"},
			},
			resActivations: map[string]float64{
				"host1": 0.9142857143, // 1 / ((320-180)/128)
				"host2": 0.0,          // no power config, should be skipped
			},
		},
		{
			name: "host without utilization knowledge should be skipped",
			stepOpts: VMWareEnergyEfficiencyStepOpts{
				HostPowerConfig: map[string]PowerConfig{
					"host1": {MinIdleWatts: 180, MaxUtilWatts: 320},
					"host2": {MinIdleWatts: 200, MaxUtilWatts: 400},
				},
			},
			knowledge: []shared.HostUtilization{
				{ComputeHost: "host1", TotalVCPUsAllocatable: 128.0},
			},
			reqHosts: []api.ExternalSchedulerHost{
				{ComputeHost: "host1"},
				{ComputeHost: "host2"},
			},
			resActivations: map[string]float64{
				"host1": 0.9142857143, // 1 / ((320-180)/128)
				"host2": 0.0,          // no utilization knowledge, should be skipped
			},
		},
		{
			name: "efficient host should have higher activation",
			stepOpts: VMWareEnergyEfficiencyStepOpts{
				HostPowerConfig: map[string]PowerConfig{
					"host1": {MinIdleWatts: 150, MaxUtilWatts: 300},
					"host2": {MinIdleWatts: 200, MaxUtilWatts: 500},
				},
			},
			knowledge: []shared.HostUtilization{
				{ComputeHost: "host1", TotalVCPUsAllocatable: 128.0},
				{ComputeHost: "host2", TotalVCPUsAllocatable: 128.0},
			},
			reqHosts: []api.ExternalSchedulerHost{
				{ComputeHost: "host1"},
				{ComputeHost: "host2"},
			},
			resActivations: map[string]float64{
				"host1": 0.8533333333, // 1 / ((300-150)/128)
				"host2": 0.512,        // 1 / ((500-200)/128)
			},
		},
	}

	for _, tc := range tests {
		hostUtilizations, err := v1alpha1.BoxFeatureList(tc.knowledge)
		if err != nil {
			t.Fatalf("failed to box host utilizations: %v", err)
		}

		step := &VMWareEnergyEfficiencyStep{}
		step.Options = tc.stepOpts
		step.Client = fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(&v1alpha1.Knowledge{
				ObjectMeta: metav1.ObjectMeta{Name: "host-utilization"},
				Status:     v1alpha1.KnowledgeStatus{Raw: hostUtilizations},
			}).
			Build()

		t.Run(tc.name, func(t *testing.T) {
			request := api.ExternalSchedulerRequest{
				Hosts: tc.reqHosts,
			}

			response, err := step.Run(slog.Default(), request)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if response == nil {
				t.Fatalf("Run() result is nil")
			}

			for host, expectedActivation := range tc.resActivations {
				actualActivation, exists := response.Activations[host]
				if !exists {
					t.Errorf("expected activation for host %s", host)
					continue
				}

				epsilon := 1e-10
				if actualActivation-expectedActivation > epsilon {
					t.Errorf("expected activation %.10f for host %s, got %.10f", expectedActivation, host, actualActivation)
				}
			}
		})
	}
}
