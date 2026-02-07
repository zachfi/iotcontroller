package global

import (
	"github.com/shimmeringbee/zcl"
)

func Register(cr *zcl.CommandRegistry) {
	cr.RegisterGlobal(ReadAttributesID, &ReadAttributes{})
	cr.RegisterGlobal(ReadAttributesResponseID, &ReadAttributesResponse{})
	cr.RegisterGlobal(WriteAttributesID, &WriteAttributes{})
	cr.RegisterGlobal(WriteAttributesUndividedID, &WriteAttributesUndivided{})
	cr.RegisterGlobal(WriteAttributesResponseID, &WriteAttributesResponse{})
	cr.RegisterGlobal(WriteAttributesNoResponseID, &WriteAttributesNoResponse{})
	cr.RegisterGlobal(ConfigureReportingID, &ConfigureReporting{})
	cr.RegisterGlobal(ConfigureReportingResponseID, &ConfigureReportingResponse{})
	cr.RegisterGlobal(ReadReportingConfigurationID, &ReadReportingConfiguration{})
	cr.RegisterGlobal(ReadReportingConfigurationResponseID, &ReadReportingConfigurationResponse{})
	cr.RegisterGlobal(ReportAttributesID, &ReportAttributes{})
	cr.RegisterGlobal(DefaultResponseID, &DefaultResponse{})
	cr.RegisterGlobal(DiscoverAttributesID, &DiscoverAttributes{})
	cr.RegisterGlobal(DiscoverAttributesResponseID, &DiscoverAttributesResponse{})
	cr.RegisterGlobal(ReadAttributesStructuredID, &ReadAttributesStructured{})
	cr.RegisterGlobal(WriteAttributesStructuredID, &WriteAttributesStructured{})
	cr.RegisterGlobal(WriteAttributesStructuredResponseID, &WriteAttributesStructuredResponse{})
	cr.RegisterGlobal(DiscoverCommandsReceivedID, &DiscoverCommandsReceived{})
	cr.RegisterGlobal(DiscoverCommandsReceivedResponseID, &DiscoverCommandsReceivedResponse{})
	cr.RegisterGlobal(DiscoverCommandsGeneratedID, &DiscoverCommandsGenerated{})
	cr.RegisterGlobal(DiscoverCommandsGeneratedResponseID, &DiscoverCommandsGeneratedResponse{})
	cr.RegisterGlobal(DiscoverAttributesExtendedID, &DiscoverAttributesExtended{})
	cr.RegisterGlobal(DiscoverAttributesExtendedResponseID, &DiscoverAttributesExtendedResponse{})
}
