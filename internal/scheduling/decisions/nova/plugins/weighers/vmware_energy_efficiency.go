package weighers

import (
	"context"
	"fmt"
	"log/slog"

	api "github.com/cobaltcore-dev/cortex/api/delegation/nova"
	"github.com/cobaltcore-dev/cortex/api/v1alpha1"
	"github.com/cobaltcore-dev/cortex/internal/knowledge/extractor/plugins/shared"
	scheduling "github.com/cobaltcore-dev/cortex/internal/scheduling/lib"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type PowerConfig struct {
	MinIdleWatts float64 `json:"minIdleWatts"`
	MaxUtilWatts float64 `json:"maxUtilWatts"`
}

func (cfg PowerConfig) Validate(key string) error {
	if cfg.MinIdleWatts < 0 {
		return fmt.Errorf("%s: minIdleWatts cannot be negative", key)
	}
	if cfg.MaxUtilWatts < 0 {
		return fmt.Errorf("%s: maxUtilWatts cannot be negative", key)
	}
	if cfg.MaxUtilWatts <= cfg.MinIdleWatts {
		return fmt.Errorf("%s: maxUtilWatts must be greater than minIdleWatts", key)
	}
	return nil
}

type VMWareEnergyEfficiencyStepOpts struct {
	HostPowerConfig map[string]PowerConfig `json:"hosts"`
}

func (e VMWareEnergyEfficiencyStepOpts) Validate() error {
	for host, cfg := range e.HostPowerConfig {
		if err := cfg.Validate(fmt.Sprintf("hosts[%s]", host)); err != nil {
			return err
		}
	}
	return nil
}

type VMWareEnergyEfficiencyStep struct {
	scheduling.BaseStep[api.ExternalSchedulerRequest, VMWareEnergyEfficiencyStepOpts]
}

func (s *VMWareEnergyEfficiencyStep) Run(traceLog *slog.Logger, request api.ExternalSchedulerRequest) (*scheduling.StepResult, error) {
	result := s.PrepareResult(request)
	result.Statistics["energy efficiency"] = s.PrepareStats(request, "W/vCPU")

	hostUtilizationKnowledge := &v1alpha1.Knowledge{}
	if err := s.Client.Get(
		context.Background(),
		client.ObjectKey{Name: "host-utilization"},
		hostUtilizationKnowledge,
	); err != nil {
		return nil, err
	}
	hostUtilizations, err := v1alpha1.
		UnboxFeatureList[shared.HostUtilization](hostUtilizationKnowledge.Status.Raw)
	if err != nil {
		return nil, err
	}
	for _, hostUtilization := range hostUtilizations {
		hostName := hostUtilization.ComputeHost
		if _, ok := result.Activations[hostName]; !ok {
			continue
		}
		cfg, ok := s.Options.HostPowerConfig[hostName]
		if !ok {
			continue
		}

		wattsPerVCPU := cfg.wattsPerVCPU(hostUtilization.TotalVCPUsAllocatable)
		result.Statistics["energy efficiency"].Subjects[hostName] = wattsPerVCPU
		result.Activations[hostUtilization.ComputeHost] = 1 / wattsPerVCPU
	}

	return result, nil
}

func (cfg PowerConfig) wattsPerVCPU(totalVCPUs float64) float64 {
	// assume linear scaling between min and max power
	return (cfg.MaxUtilWatts - cfg.MinIdleWatts) / totalVCPUs
}
