package zcl

import "github.com/shimmeringbee/zigbee"

/*
 * Zigbee Cluster List, as per Cluster Library Specification, Revision 8 (December 2918). Now branded dotdot.
 * Downloaded From: https://zigbeealliance.org/wp-content/uploads/2021/10/07-5123-08-Zigbee-Cluster-Library.pdf
 */

const (
	BasicId                                   = zigbee.ClusterID(0x0000)
	PowerConfigurationId                      = zigbee.ClusterID(0x0001)
	DeviceTemperatureConfigurationId          = zigbee.ClusterID(0x0002)
	IdentifyId                                = zigbee.ClusterID(0x0003)
	GroupsId                                  = zigbee.ClusterID(0x0004)
	ScenesId                                  = zigbee.ClusterID(0x0005)
	OnOffId                                   = zigbee.ClusterID(0x0006)
	OnOffSwitchConfigurationId                = zigbee.ClusterID(0x0007)
	LevelControlId                            = zigbee.ClusterID(0x0008)
	AlarmsId                                  = zigbee.ClusterID(0x0009)
	TimeId                                    = zigbee.ClusterID(0x000a)
	RSSILocationId                            = zigbee.ClusterID(0x000b)
	AnalogInputBasicId                        = zigbee.ClusterID(0x000c)
	AnalogOutputBasicId                       = zigbee.ClusterID(0x000d)
	AnalogValveBasicId                        = zigbee.ClusterID(0x000e)
	BinaryInputBasicId                        = zigbee.ClusterID(0x000f)
	BinaryOutputBasicId                       = zigbee.ClusterID(0x0010)
	BinaryValueBasicId                        = zigbee.ClusterID(0x0011)
	MultistateInputBasicId                    = zigbee.ClusterID(0x0012)
	MultistateOutputBasicId                   = zigbee.ClusterID(0x0013)
	MultistateValueBasicId                    = zigbee.ClusterID(0x0014)
	CommissioningId                           = zigbee.ClusterID(0x0015)
	PartitionId                               = zigbee.ClusterID(0x0016)
	OTAUpgradeId                              = zigbee.ClusterID(0x0019)
	PowerProfileId                            = zigbee.ClusterID(0x001a)
	EN50523ApplianceControlId                 = zigbee.ClusterID(0x001b)
	PulseWidthModulationId                    = zigbee.ClusterID(0x001c)
	PollControlId                             = zigbee.ClusterID(0x0020)
	MobileDeviceConfigurationClusterId        = zigbee.ClusterID(0x0022)
	NeighborCleaningClusterId                 = zigbee.ClusterID(0x0023)
	NearestGatewayClusterId                   = zigbee.ClusterID(0x0024)
	KeepAliveId                               = zigbee.ClusterID(0x0025)
	ShadeConfigurationId                      = zigbee.ClusterID(0x0100)
	DoorLockId                                = zigbee.ClusterID(0x0101)
	WindowCoveringId                          = zigbee.ClusterID(0x0102)
	PumpConfigurationAndControlId             = zigbee.ClusterID(0x0200)
	ThermostatId                              = zigbee.ClusterID(0x0201)
	FanControlId                              = zigbee.ClusterID(0x0202)
	DehumidificationControlId                 = zigbee.ClusterID(0x0203)
	ThermostatUserInterfaceConfigurationId    = zigbee.ClusterID(0x0204)
	ColorControlId                            = zigbee.ClusterID(0x0300)
	BallastConfigurationId                    = zigbee.ClusterID(0x0301)
	IlluminanceMeasurementId                  = zigbee.ClusterID(0x0400)
	IlluminanceLevelSensingId                 = zigbee.ClusterID(0x0401)
	TemperatureMeasurementId                  = zigbee.ClusterID(0x0402)
	PressureMeasurementId                     = zigbee.ClusterID(0x0403)
	FlowMeasurementId                         = zigbee.ClusterID(0x0404)
	RelativeHumidityMeasurementId             = zigbee.ClusterID(0x0405)
	OccupancySensingId                        = zigbee.ClusterID(0x0406)
	LeafWetnessId                             = zigbee.ClusterID(0x0407)
	SoilMoistureId                            = zigbee.ClusterID(0x0408)
	PHMeasurementId                           = zigbee.ClusterID(0x0409)
	ElectricalConductivityId                  = zigbee.ClusterID(0x040a)
	WindSpeedMeasurementId                    = zigbee.ClusterID(0x040b)
	AirConcentrationCarbonMonoxideId          = zigbee.ClusterID(0x040c)
	AirConcentrationCarbonDioxideId           = zigbee.ClusterID(0x040d)
	AirConcentrationEthyleneId                = zigbee.ClusterID(0x040e)
	AirConcentrationEthyleneOxideId           = zigbee.ClusterID(0x040f)
	AirConcentrationHydrogenId                = zigbee.ClusterID(0x0410)
	AirConcentrationHydrogenSulfideId         = zigbee.ClusterID(0x0411)
	AirConcentrationNitricOxideId             = zigbee.ClusterID(0x0412)
	AirConcentrationNitrogenDioxideId         = zigbee.ClusterID(0x0413)
	AirConcentrationOxygenId                  = zigbee.ClusterID(0x0414)
	AirConcentrationOzoneId                   = zigbee.ClusterID(0x0415)
	AirConcentrationSulfurDioxideId           = zigbee.ClusterID(0x0416)
	WaterConcentrationDissolvedOxygenId       = zigbee.ClusterID(0x0417)
	WaterConcentrationBromateId               = zigbee.ClusterID(0x0418)
	WaterConcentrationChloraminesId           = zigbee.ClusterID(0x0419)
	WaterConcentrationChlorineId              = zigbee.ClusterID(0x041a)
	WaterConcentrationFecalcoliformEColiId    = zigbee.ClusterID(0x041b)
	WaterConcentrationFluorideId              = zigbee.ClusterID(0x041c)
	WaterConcentrationHaloaceticAcidsId       = zigbee.ClusterID(0x041d)
	WaterConcentrationTotalTrihalomethanesId  = zigbee.ClusterID(0x041e)
	WaterConcentrationTotalColiformBacteriaId = zigbee.ClusterID(0x041f)
	WaterConcentrationTurbidityId             = zigbee.ClusterID(0x0420)
	WaterConcentrationCopperId                = zigbee.ClusterID(0x0421)
	WaterConcentrationLeadId                  = zigbee.ClusterID(0x0422)
	WaterConcentrationManganeseId             = zigbee.ClusterID(0x0423)
	WaterConcentrationSulfateId               = zigbee.ClusterID(0x0424)
	WaterConcentrationBromodichloromethaneId  = zigbee.ClusterID(0x0425)
	WaterConcentrationBromoformId             = zigbee.ClusterID(0x0426)
	WaterConcentrationChlorodibromomethaneId  = zigbee.ClusterID(0x0427)
	WaterConcentrationChloroformId            = zigbee.ClusterID(0x0428)
	WaterConcentrationSodiumId                = zigbee.ClusterID(0x0429)
	AirConcentrationPM25Id                    = zigbee.ClusterID(0x042a)
	AirConcentrationFormaldehydeId            = zigbee.ClusterID(0x042b)
	IASZoneId                                 = zigbee.ClusterID(0x0500)
	IASAncillaryControlEquipmentId            = zigbee.ClusterID(0x0501)
	IASWarningDevicesId                       = zigbee.ClusterID(0x0502)
	GenericTunnelId                           = zigbee.ClusterID(0x0600)
	BACnetProtocolTunnelId                    = zigbee.ClusterID(0x0601)
	AnalogInputBACnetRegularId                = zigbee.ClusterID(0x0602)
	AnalogInputBACnetExtendedId               = zigbee.ClusterID(0x0603)
	AnalogOutputBACnetRegularId               = zigbee.ClusterID(0x0604)
	AnalogOutputBACnetExtendedId              = zigbee.ClusterID(0x0605)
	AnalogValueBACnetRegularId                = zigbee.ClusterID(0x0606)
	AnalogValueBACnetExtendedId               = zigbee.ClusterID(0x0607)
	BinaryInputBACnetRegularId                = zigbee.ClusterID(0x0608)
	BinaryInputBACnetExtendedId               = zigbee.ClusterID(0x0609)
	BinaryOutputBACnetRegularId               = zigbee.ClusterID(0x060a)
	BinaryOutputBACnetExtendedId              = zigbee.ClusterID(0x060b)
	BinaryValueBACnetRegularId                = zigbee.ClusterID(0x060c)
	BinaryValueBACnetExtendedId               = zigbee.ClusterID(0x060d)
	MultistateInputBACnetRegularId            = zigbee.ClusterID(0x060e)
	MultistateInputBACnetExtendedId           = zigbee.ClusterID(0x060f)
	MultistateOutputBACnetRegularId           = zigbee.ClusterID(0x0610)
	MultistateOutputBACnetExtendedId          = zigbee.ClusterID(0x0611)
	MultistateValueBACnetRegularId            = zigbee.ClusterID(0x0612)
	MultistateValueBACnetExtendedId           = zigbee.ClusterID(0x0613)
	ISO11073ProtocolTunnelId                  = zigbee.ClusterID(0x0614)
	ISO7816TunnelId                           = zigbee.ClusterID(0x0615)
	RetailTunnelClusterId                     = zigbee.ClusterID(0x0617)
	PriceId                                   = zigbee.ClusterID(0x0700)
	DemandResponseAndLoadControlId            = zigbee.ClusterID(0x0701)
	MeteringId                                = zigbee.ClusterID(0x0702)
	MessagingId                               = zigbee.ClusterID(0x0703)
	TunnelingId                               = zigbee.ClusterID(0x0704)
	PrepaymentId                              = zigbee.ClusterID(0x0705)
	CalendarId                                = zigbee.ClusterID(0x0707)
	DeviceManagementId                        = zigbee.ClusterID(0x0708)
	EventsId                                  = zigbee.ClusterID(0x0709)
	SubGHzId                                  = zigbee.ClusterID(0x070b)
	KeyEstablishmentId                        = zigbee.ClusterID(0x0800)
	InformationId                             = zigbee.ClusterID(0x0900)
	VoiceOverZigBeeId                         = zigbee.ClusterID(0x0904)
	ChattingId                                = zigbee.ClusterID(0x0905)
	EN50523ApplianceIdentificationId          = zigbee.ClusterID(0x0b00)
	MeterIdentificationId                     = zigbee.ClusterID(0x0b01)
	EN50523ApplianceEventsAndAlertsId         = zigbee.ClusterID(0x0b02)
	EN50523ApplianceStatisticsId              = zigbee.ClusterID(0x0b03)
	ElectricalMeasurementId                   = zigbee.ClusterID(0x0b04)
	DiagnosticsId                             = zigbee.ClusterID(0x0b05)
	TouchlinkId                               = zigbee.ClusterID(0x1000)
)

var ClusterList = map[zigbee.ClusterID]string{
	BasicId:                                   "Basic",
	PowerConfigurationId:                      "Power Configuration",
	DeviceTemperatureConfigurationId:          "Device Temperature Configuration",
	IdentifyId:                                "Identify",
	GroupsId:                                  "Groups",
	ScenesId:                                  "Scenes",
	OnOffId:                                   "On/Off",
	OnOffSwitchConfigurationId:                "On/Off Switch Configuration",
	LevelControlId:                            "Level Control",
	AlarmsId:                                  "Alarms",
	TimeId:                                    "Time",
	RSSILocationId:                            "RSSI Location",
	AnalogInputBasicId:                        "Analog Input (Basic)",
	AnalogOutputBasicId:                       "Analog Output (Basic)",
	AnalogValveBasicId:                        "Analog Valve (Basic)",
	BinaryInputBasicId:                        "Binary Input (Basic)",
	BinaryOutputBasicId:                       "Binary Output (Basic)",
	BinaryValueBasicId:                        "Binary Value (Basic)",
	MultistateInputBasicId:                    "Multistate Input (Basic)",
	MultistateOutputBasicId:                   "Multistate Output (Basic)",
	MultistateValueBasicId:                    "Multistate Value (Basic)",
	CommissioningId:                           "Commissioning",
	PartitionId:                               "Partition",
	OTAUpgradeId:                              "OTA Upgrade",
	PowerProfileId:                            "Power Profile",
	EN50523ApplianceControlId:                 "EN50523 Appliance Control",
	PulseWidthModulationId:                    "Pulse Width Modulation",
	PollControlId:                             "Poll Control",
	MobileDeviceConfigurationClusterId:        "Mobile Device Configuration Cluster",
	NeighborCleaningClusterId:                 "Neighbor Cleaning Cluster",
	NearestGatewayClusterId:                   "Nearest Gateway Cluster",
	KeepAliveId:                               "Keep Alive",
	ShadeConfigurationId:                      "Shade Configuration",
	DoorLockId:                                "Door Lock",
	WindowCoveringId:                          "Window Covering",
	PumpConfigurationAndControlId:             "Pump Configuration and Control",
	ThermostatId:                              "Thermostat",
	FanControlId:                              "Fan Control",
	DehumidificationControlId:                 "Dehumidification Control",
	ThermostatUserInterfaceConfigurationId:    "Thermostat User Interface Configuration",
	ColorControlId:                            "Color Control",
	BallastConfigurationId:                    "Ballast Configuration",
	IlluminanceMeasurementId:                  "Illuminance Measurement",
	IlluminanceLevelSensingId:                 "Illuminance Level Sensing",
	TemperatureMeasurementId:                  "Temperature Measurement",
	PressureMeasurementId:                     "Pressure Measurement",
	FlowMeasurementId:                         "Flow Measurement",
	RelativeHumidityMeasurementId:             "Relative Humidity Measurement",
	OccupancySensingId:                        "Occupancy Sensing",
	LeafWetnessId:                             "Leaf Wetness",
	SoilMoistureId:                            "Soil Moisture",
	PHMeasurementId:                           "pH Measurement",
	ElectricalConductivityId:                  "Electrical Conductivity",
	WindSpeedMeasurementId:                    "Wind Speed Measurement",
	AirConcentrationCarbonMonoxideId:          "Carbon Monoxide Concentration (Air)",
	AirConcentrationCarbonDioxideId:           "Carbon Dioxide Concentration (Air)",
	AirConcentrationEthyleneId:                "Ethylene Concentration (Air)",
	AirConcentrationEthyleneOxideId:           "Ethylene Oxide Concentration (Air)",
	AirConcentrationHydrogenId:                "Hydrogen Concentration (Air)",
	AirConcentrationHydrogenSulfideId:         "Hydrogen Sulfide Concentration (Air)",
	AirConcentrationNitricOxideId:             "Nitric Oxide Concentration (Air)",
	AirConcentrationNitrogenDioxideId:         "Nitrogen Dioxide Concentration (Air)",
	AirConcentrationOxygenId:                  "Oxygen Concentration (Air)",
	AirConcentrationOzoneId:                   "Ozone Concentration (Air)",
	AirConcentrationSulfurDioxideId:           "Sulfur Dioxide Concentration (Air)",
	WaterConcentrationDissolvedOxygenId:       "Dissolved Oxygen Concentration (Water)",
	WaterConcentrationBromateId:               "Bromate Concentration (Water)",
	WaterConcentrationChloraminesId:           "Chloramines Concentration (Water)",
	WaterConcentrationChlorineId:              "Chlorine Concentration (Water)",
	WaterConcentrationFecalcoliformEColiId:    "Fecal coliform & E. Coli Concentration (Water)",
	WaterConcentrationFluorideId:              "Fluoride Concentration (Water)",
	WaterConcentrationHaloaceticAcidsId:       "Haloacetic Acids Concentration (Water)",
	WaterConcentrationTotalTrihalomethanesId:  "Total Trihalomethanes Concentration (Water)",
	WaterConcentrationTotalColiformBacteriaId: "Total Coliform Bacteria Concentration (Water)",
	WaterConcentrationTurbidityId:             "Turbidity Concentration (Water)",
	WaterConcentrationCopperId:                "Copper Concentration (Water)",
	WaterConcentrationLeadId:                  "Lead Concentration (Water)",
	WaterConcentrationManganeseId:             "Manganese Concentration (Water)",
	WaterConcentrationSulfateId:               "Sulfate Concentration (Water)",
	WaterConcentrationBromodichloromethaneId:  "Bromodichloromethane Concentration (Water)",
	WaterConcentrationBromoformId:             "Bromoform Concentration (Water)",
	WaterConcentrationChlorodibromomethaneId:  "Chlorodibromomethane Concentration (Water)",
	WaterConcentrationChloroformId:            "Chloroform Concentration (Water)",
	WaterConcentrationSodiumId:                "Sodium Concentration (Water)",
	AirConcentrationPM25Id:                    "PM2.5 Concentration (Air)",
	AirConcentrationFormaldehydeId:            "Formaldehyde Concentration (Air)",
	IASZoneId:                                 "IAS Zone",
	IASAncillaryControlEquipmentId:            "IAS Ancillary Control Equipment",
	IASWarningDevicesId:                       "IAS Warning Devices",
	GenericTunnelId:                           "Generic Tunnel",
	BACnetProtocolTunnelId:                    "BACnet Protocol Tunnel",
	AnalogInputBACnetRegularId:                "Analog Input (BACnet Regular)",
	AnalogInputBACnetExtendedId:               "Analog Input (BACnet Extended)",
	AnalogOutputBACnetRegularId:               "Analog Output (BACnet Regular)",
	AnalogOutputBACnetExtendedId:              "Analog Output (BACnet Extended)",
	AnalogValueBACnetRegularId:                "Analog Value (BACnet Regular)",
	AnalogValueBACnetExtendedId:               "Analog Value (BACnet Extended)",
	BinaryInputBACnetRegularId:                "Binary Input (BACnet Regular)",
	BinaryInputBACnetExtendedId:               "Binary Input (BACnet Extended)",
	BinaryOutputBACnetRegularId:               "Binary Output (BACnet Regular)",
	BinaryOutputBACnetExtendedId:              "Binary Output (BACnet Extended)",
	BinaryValueBACnetRegularId:                "Binary Value (BACnet Regular)",
	BinaryValueBACnetExtendedId:               "Binary Value (BACnet Extended)",
	MultistateInputBACnetRegularId:            "Multistate Input (BACnet Regular)",
	MultistateInputBACnetExtendedId:           "Multistate Input (BACnet Extended)",
	MultistateOutputBACnetRegularId:           "Multistate Output (BACnet Regular)",
	MultistateOutputBACnetExtendedId:          "Multistate Output (BACnet Extended)",
	MultistateValueBACnetRegularId:            "Multistate Value (BACnet Regular)",
	MultistateValueBACnetExtendedId:           "Multistate Value (BACnet Extended)",
	ISO11073ProtocolTunnelId:                  "ISO11073 Protocol Tunnel",
	ISO7816TunnelId:                           "ISO7816 Tunnel",
	RetailTunnelClusterId:                     "Retail Tunnel Cluster",
	PriceId:                                   "Price",
	DemandResponseAndLoadControlId:            "Demand Response and Load Control",
	MeteringId:                                "Metering",
	MessagingId:                               "Messaging",
	TunnelingId:                               "Tunneling",
	PrepaymentId:                              "Prepayment",
	CalendarId:                                "Calendar",
	DeviceManagementId:                        "Device Management",
	EventsId:                                  "Events",
	SubGHzId:                                  "Sub-GHz",
	KeyEstablishmentId:                        "Key Establishment",
	InformationId:                             "Information",
	VoiceOverZigBeeId:                         "Voice Over ZigBee",
	ChattingId:                                "Chatting",
	EN50523ApplianceIdentificationId:          "EN50523 Appliance Identification",
	MeterIdentificationId:                     "Meter Identification",
	EN50523ApplianceEventsAndAlertsId:         "EN50523 Appliance Events and Alerts",
	EN50523ApplianceStatisticsId:              "EN50523 Appliance Statistics",
	ElectricalMeasurementId:                   "Electrical Measurement",
	DiagnosticsId:                             "Diagnostics",
	TouchlinkId:                               "Touchlink",
}
