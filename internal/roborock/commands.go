package roborock

// Command names for the V1 protocol, as exposed by the device RPC layer.
// The vacuum answers "unknown_method" for anything it does not implement.
const (
	CmdAppStart                = "app_start"
	CmdAppPause                = "app_pause"
	CmdAppStop                 = "app_stop"
	CmdAppCharge               = "app_charge"
	CmdAppSpot                 = "app_spot"
	CmdAppGotoTarget           = "app_goto_target"
	CmdAppZonedClean           = "app_zoned_clean"
	CmdAppSegmentClean         = "app_segment_clean"
	CmdAppRCStart              = "app_rc_start"
	CmdAppRCMove               = "app_rc_move"
	CmdAppRCEnd                = "app_rc_end"
	CmdAppRCStop               = "app_rc_stop"
	CmdAppStartCollectDust     = "app_start_collect_dust"
	CmdAppStopCollectDust      = "app_stop_collect_dust"
	CmdAppStartWash            = "app_start_wash"
	CmdAppStopWash             = "app_stop_wash"
	CmdAppWakeupRobot          = "app_wakeup_robot"
	CmdAppStat                 = "app_stat"
	CmdAppResumeBuildMap       = "app_resume_build_map"
	CmdFindMe                  = "find_me"
	CmdGetStatus               = "get_status"
	CmdGetInitStatus           = "app_get_init_status"
	CmdGetConsumable           = "get_consumable"
	CmdResetConsumable         = "reset_consumable"
	CmdGetRoomMapping          = "get_room_mapping"
	CmdGetCleanRecord          = "get_clean_record"
	CmdGetCleanSummary         = "get_clean_summary"
	CmdGetCleanRecordMap       = "get_clean_record_map"
	CmdGetCleanSequence        = "get_clean_sequence"
	CmdGetSegmentStatus        = "get_segment_status"
	CmdGetNetworkInfo          = "get_network_info"
	CmdGetTimezone             = "get_timezone"
	CmdGetLocale               = "app_get_locale"
	CmdGetTimer                = "get_timer"
	CmdGetTimerDetail          = "get_timer_detail"
	CmdGetTimerSummary         = "get_timer_summary"
	CmdGetDndTimer             = "get_dnd_timer"
	CmdSetDndTimer             = "set_dnd_timer"
	CmdCloseDndTimer           = "close_dnd_timer"
	CmdGetCustomMode           = "get_custom_mode"
	CmdSetCustomMode           = "set_custom_mode"
	CmdGetSoundVolume          = "get_sound_volume"
	CmdSetSoundVolume          = "set_sound_volume"
	CmdChangeSoundVolume       = "change_sound_volume"
	CmdTestSoundVolume         = "test_sound_volume"
	CmdGetChildLockStatus      = "get_child_lock_status"
	CmdSetChildLockStatus      = "set_child_lock_status"
	CmdGetLedStatus            = "get_led_status"
	CmdSetLedStatus            = "set_led_status"
	CmdGetCarpetMode           = "get_carpet_mode"
	CmdSetCarpetMode           = "set_carpet_mode"
	CmdGetCarpetCleanMode      = "get_carpet_clean_mode"
	CmdSetCarpetCleanMode      = "set_carpet_clean_mode"
	CmdGetMopMode              = "get_custom_mode" // devices report mop mode through custom mode
	CmdSetMopMode              = "set_mop_mode"
	CmdGetWaterBoxCustomMode   = "get_water_box_custom_mode"
	CmdSetWaterBoxCustomMode   = "set_water_box_custom_mode"
	CmdGetCollisionAvoidStatus = "get_collision_avoid_status"
	CmdSetCollisionAvoidStatus = "set_collision_avoid_status"
	CmdGetMapStatus            = "get_map_status"
	CmdGetFwFeatures           = "get_fw_features"
	CmdGetDockInfo             = "get_dock_info"
	CmdGetMultiMapsList        = "get_multi_maps_list"
	CmdGetProp                 = "get_prop"
	CmdGetMultiMap             = "get_multi_map"
	CmdGetMap                  = "get_map"
	CmdGetPersistMap           = "get_persist_map"
	CmdGetDynamicMapDiff       = "get_dynamic_map_diff"
	CmdGetCurrentSound         = "get_current_sound"
	CmdGetFlowLedStatus        = "get_flow_led_status"
	CmdGetSerialNumber         = "get_serial_number"
	CmdGetRechargeTime         = "get_fan_motor_work_timeout"
	CmdSetFanMotorWorkTimeout  = "set_fan_motor_work_timeout"
	CmdGetMultiRoomMaterial    = "get_multi_room_material"
	CmdResolveError            = "resolve_error"
	CmdPlayAudio               = "play_audio"
	CmdGetRoomMappingV2        = "get_multi_maps_list"
	CmdGetServerTimer          = "get_server_timer"
	CmdSetServerTimer          = "set_server_timer"
	CmdDeleteServerTimer       = "del_server_timer"
	CmdUpdateServerTimer       = "upd_server_timer"
	CmdGetMapCalibration       = "get_map_calibration"
	CmdGetRobotSetting         = "app_get_robot_setting"
	CmdSetRobotSetting         = "app_set_robot_setting"
	CmdGetSceneValidTIDs       = "get_scenes_valid_tids"
	CmdGetMatterStatus         = "matter.get_status"
	CmdGetRandomPKey           = "get_random_pkey"
	CmdGetWashTowelParams      = "get_wash_towel_params"
	CmdSetWashTowelParams      = "set_wash_towel_params"
	CmdGetWashTowelMode        = "get_wash_towel_mode"
	CmdSetWashTowelMode        = "set_wash_towel_mode"
	CmdGetDustCollectionMode   = "get_dust_collection_mode"
	CmdSetDustCollectionMode   = "set_dust_collection_mode"
	CmdGetAutoEmptyStatus      = "get_dust_collection_switch_status"
	CmdSetAutoEmptyStatus      = "set_dust_collection_switch_status"
	CmdGetAirdryHours          = "get_airdry_hours"
	CmdSetAirdryHours          = "set_airdry_hours"
	CmdGetWashWaterTemperature = "get_wash_water_temperature"
	CmdSetWashWaterTemperature = "set_wash_water_temperature"
	CmdGetHotWaterTemp         = "get_wash_water_temperature"
	CmdEnableLogUpload         = "enable_log_upload"
	CmdGetLogUploadStatus      = "get_log_upload_status"
	CmdGetDeviceSdp            = "get_device_sdp"
	CmdGetDeviceIce            = "get_device_ice"
)

// ReadOnlyCommands lists methods that only read device state. The CLI
// documents them as inspection commands and allows them without --force.
var ReadOnlyCommands = map[string]bool{
	"prop.get": true, "service.get_map_list": true, "service.get_record_list": true,
	"app_get_dryer_setting": true, "get_smart_wash_params": true,
	CmdGetStatus: true, CmdGetInitStatus: true, CmdAppStat: true,
	CmdGetConsumable: true, CmdGetRoomMapping: true, CmdGetCleanRecord: true,
	CmdGetCleanSummary: true, CmdGetCleanSequence: true, CmdGetSegmentStatus: true,
	CmdGetNetworkInfo: true, CmdGetTimezone: true, CmdGetLocale: true,
	CmdGetTimer: true, CmdGetTimerDetail: true, CmdGetTimerSummary: true,
	CmdGetDndTimer: true, CmdGetCustomMode: true, CmdGetSoundVolume: true,
	CmdGetChildLockStatus: true, CmdGetLedStatus: true, CmdGetCarpetMode: true,
	CmdGetCarpetCleanMode: true, CmdGetWaterBoxCustomMode: true,
	CmdGetCollisionAvoidStatus: true, CmdGetMapStatus: true, CmdGetFwFeatures: true,
	CmdGetDockInfo: true, CmdGetMultiMapsList: true, CmdGetProp: true,
	CmdGetMultiMap: true, CmdGetCurrentSound: true, CmdGetFlowLedStatus: true,
	CmdGetSerialNumber: true, CmdGetMapCalibration: true, CmdGetRobotSetting: true,
	CmdGetSceneValidTIDs: true, CmdGetMatterStatus: true, CmdGetRandomPKey: true,
	CmdGetWashTowelParams: true, CmdGetWashTowelMode: true,
	CmdGetDustCollectionMode: true, CmdGetAutoEmptyStatus: true,
	CmdGetAirdryHours: true, CmdGetWashWaterTemperature: true,
	CmdGetLogUploadStatus: true, CmdGetDeviceSdp: true, CmdGetDeviceIce: true,
}

// Vacuum state codes reported in get_status.state.
var stateNames = map[int]string{
	0: "unknown", 1: "starting", 2: "charger_disconnected", 3: "idle",
	4: "remote_control_active", 5: "cleaning", 6: "returning_home",
	7: "manual_mode", 8: "charging", 9: "charging_problem", 10: "paused",
	11: "spot_cleaning", 12: "error", 13: "shutting_down", 14: "updating",
	15: "docking", 16: "going_to_target", 17: "zoned_cleaning",
	18: "segment_cleaning", 22: "emptying_the_bin", 23: "washing_the_mop",
	25: "washing_the_mop", 26: "going_to_wash_the_mop", 28: "in_call",
	29: "mapping", 30: "egg_attack", 32: "patrol", 33: "attaching_the_mop",
	34: "detaching_the_mop", 100: "charging_complete", 101: "device_offline",
	103: "locked", 202: "air_drying_stopping", 6301: "mopping",
	6302: "mop_cleaning", 6303: "mop_mopping", 6304: "segment_mopping",
	6305: "segment_clean_mop_cleaning", 6306: "segment_clean_mop_mopping",
	6307: "zoned_mopping", 6308: "zoned_clean_mop_cleaning",
	6309: "zoned_clean_mop_mopping", 6310: "back_to_dock_washing_duster",
}

// Error codes reported in get_status.error_code.
var errorNames = map[int]string{
	0: "none", 1: "lidar_blocked", 2: "bumper_stuck", 3: "wheels_suspended",
	4: "cliff_sensor_error", 5: "main_brush_jammed", 6: "side_brush_jammed",
	7: "wheels_jammed", 8: "robot_trapped", 9: "no_dustbin",
	10: "strainer_error", 11: "compass_error", 12: "low_battery",
	13: "charging_error", 14: "battery_error", 15: "wall_sensor_dirty",
	16: "robot_tilted", 17: "side_brush_error", 18: "fan_error",
	19: "dock_not_connected", 20: "optical_flow_sensor_dirty",
	21: "vertical_bumper_pressed", 22: "dock_locator_error",
	23: "return_to_dock_fail", 24: "nogo_zone_detected", 25: "visual_sensor",
	26: "light_touch", 27: "vibrarise_jammed", 28: "robot_on_carpet",
	29: "filter_blocked", 30: "invisible_wall_detected", 31: "cannot_cross_carpet",
	32: "internal_error", 34: "collect_dust_error", 35: "collect_dust_voltage_error",
	36: "mopping_roller_jammed", 37: "wash_roller_not_lowered",
	38: "check_clean_water_tank", 39: "check_dirty_water_tank",
	40: "reinstall_water_filter", 41: "clean_water_tank_empty",
	42: "check_water_filter_installed", 43: "positioning_button_error",
	44: "clean_dock_water_filter", 45: "mopping_roller_jammed",
	48: "water_fill_error", 49: "water_drain_error",
	51: "temperature_protection", 52: "clean_carousel_exception",
	53: "clean_carousel_water_full", 54: "water_carriage_drop",
	55: "check_clean_carousel", 56: "audio_error",
}

// Fan speed codes vary per model generation.
var (
	fanSpeedV1 = map[int]string{38: "silent", 60: "standard", 77: "medium", 90: "turbo"}
	fanSpeedV2 = map[int]string{101: "silent", 102: "balanced", 103: "turbo", 104: "max", 105: "gentle", 106: "auto"}
	fanSpeedV3 = map[int]string{38: "silent", 60: "standard", 75: "medium", 100: "turbo"}
	fanSpeedE2 = map[int]string{41: "gentle", 50: "silent", 68: "standard", 79: "medium", 100: "turbo"}
)

// Mop/water box intensity codes.
var mopIntensity = map[int]string{
	200: "off", 201: "low", 202: "medium", 203: "high", 207: "custom",
}

// StateName returns a readable state name for a device state code.
func StateName(code int) string {
	if name, ok := stateNames[code]; ok {
		return name
	}
	return "unknown"
}

// ErrorName returns a readable error name for an error code.
func ErrorName(code int) string {
	if name, ok := errorNames[code]; ok {
		return name
	}
	return "unknown"
}

// FanSpeedName maps a fan power code to a readable name. It tries the modern
// numeric set first, then the older percentage sets.
func FanSpeedName(code int) string {
	if name, ok := fanSpeedV2[code]; ok {
		return name
	}
	if name, ok := fanSpeedV1[code]; ok {
		return name
	}
	if name, ok := fanSpeedV3[code]; ok {
		return name
	}
	if name, ok := fanSpeedE2[code]; ok {
		return name
	}
	return ""
}

// MopModeName maps a water box mode code to a readable name.
func MopModeName(code int) string {
	if name, ok := mopIntensity[code]; ok {
		return name
	}
	return ""
}
