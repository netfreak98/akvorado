package static

import (
	"context"

	"akvorado/common/helpers"
	"akvorado/common/remotedatasourcefetcher"
	"akvorado/inlet/metadata/provider"
)

type exporterInfo struct {
	provider.Exporter `mapstructure:",squash" yaml:",inline"`
	ExporterSubnet    string
	// Default is used if not empty for any unknown ifindexes
	Default provider.Interface `validate:"omitempty"`
	// IfIndexes is a map from interface indexes to interfaces
	Interfaces []exporterInterface `validate:"omitempty"`
}

type exporterInterface struct {
	IfIndex            uint
	provider.Interface `validate:"omitempty,dive" mapstructure:",squash"`
}

func (i exporterInfo) toExporterConfiguration() ExporterConfiguration {
	ifindexMap := map[uint]provider.Interface{}
	for _, iface := range i.Interfaces {
		ifindexMap[iface.IfIndex] = iface.Interface
	}

	return ExporterConfiguration{
		Exporter:  i.Exporter,
		Default:   i.Default,
		IfIndexes: ifindexMap,
	}
}

// initStaticExporters initializes the reconciliation map for exporter configurations
// with the static prioritized data from exporters' Configuration.
func (p *Provider) initStaticExporters() {
	staticExportersMap := p.exporters.Load().ToMap()
	staticExporters := make([]exporterInfo, 0, len(staticExportersMap))
	for subnet, config := range staticExportersMap {
		interfaces := make([]exporterInterface, 0, len(config.IfIndexes))
		for ifindex, iface := range config.IfIndexes {
			interfaces = append(interfaces, exporterInterface{
				IfIndex:   ifindex,
				Interface: iface,
			})
		}
		staticExporters = append(
			staticExporters,
			exporterInfo{
				Exporter: provider.Exporter{
					Name: config.Name,
				},
				ExporterSubnet: subnet,
				Default:        config.Default,
				Interfaces:     interfaces,
			},
		)
	}
	p.exportersMap["static"] = staticExporters
}

// UpdateRemoteDataSource updates a remote metadata exporters source. It returns the
// number of exporters retrieved.
func (p *Provider) UpdateRemoteDataSource(ctx context.Context, name string, source remotedatasourcefetcher.RemoteDataSource) (int, error) {
	results, err := p.exporterSourcesFetcher.Fetch(ctx, name, source)
	if err != nil {
		return 0, err
	}
	finalMap := map[string]ExporterConfiguration{}
	p.exportersLock.Lock()
	p.exportersMap[name] = results
	for id, results := range p.exportersMap {
		if id == "static" {
			continue
		}
		for _, exporterData := range results {
			exporterSubnet, err := helpers.SubnetMapParseKey(exporterData.ExporterSubnet)
			if err != nil {
				p.r.Err(err).Msg("failed to decode subnet")
				continue
			}
			// Concurrency for same Exporter config across multiple remote data sources is not handled
			finalMap[exporterSubnet] = exporterData.toExporterConfiguration()
		}
	}
	for _, exporterData := range p.exportersMap["static"] {
		exporterSubnet, err := helpers.SubnetMapParseKey(exporterData.ExporterSubnet)
		if err != nil {
			p.r.Err(err).Msg("failed to decode subnet")
			continue
		}
		// This overrides duplicates config for an Exporter if it's also defined as static
		finalMap[exporterSubnet] = exporterData.toExporterConfiguration()
	}
	p.exportersLock.Unlock()
	exporters, err := helpers.NewSubnetMap[ExporterConfiguration](finalMap)
	if err != nil {
		return 0, err
	}
	p.exporters.Swap(exporters)
	return len(results), nil
}
