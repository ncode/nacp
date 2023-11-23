package postgres

import future.keywords


test_patch_vault_policy if {

    patch_ops := patch with input as {
		"Affinities": null,
		"AllAtOnce": false,
		"Constraints": null,
		"ConsulNamespace": "",
		"ConsulToken": "",
		"CreateIndex": 289,
		"Datacenters": ["*"],
		"DispatchIdempotencyToken": "",
		"Dispatched": false,
		"ID": "app",
		"JobModifyIndex": 459,
		"Meta": null,
		"ModifyIndex": 468,
		"Multiregion": null,
		"Name": "app",
		"Namespace": "default",
		"NomadTokenID": "",
		"ParameterizedJob": null,
		"ParentID": "",
		"Payload": null,
		"Periodic": null,
		"Priority": 50,
		"Region": "global",
		"Spreads": null,
		"Stable": true,
		"Status": "running",
		"StatusDescription": "",
		"Stop": false,
		"SubmitTime": 1679091610901210000,
		"TaskGroups": [{
			"Affinities": null,
			"Constraints": null,
			"Consul": {"Namespace": ""},
			"Count": 1,
			"EphemeralDisk": {
				"Migrate": false,
				"SizeMB": 300,
				"Sticky": false,
			},
			"MaxClientDisconnect": null,
			"Meta": null,
			"Migrate": {
				"HealthCheck": "checks",
				"HealthyDeadline": 300000000000,
				"MaxParallel": 1,
				"MinHealthyTime": 10000000000,
			},
			"Name": "app",
			"Networks": null,
			"ReschedulePolicy": {
				"Attempts": 0,
				"Delay": 30000000000,
				"DelayFunction": "exponential",
				"Interval": 0,
				"MaxDelay": 3600000000000,
				"Unlimited": true,
			},
			"RestartPolicy": {
				"Attempts": 2,
				"Delay": 15000000000,
				"Interval": 1800000000000,
				"Mode": "fail",
			},
			"Scaling": null,
			"Services": null,
			"ShutdownDelay": null,
			"Spreads": null,
			"StopAfterClientDisconnect": null,
			"Tasks": [{
				"Affinities": null,
				"Artifacts": null,
				"CSIPluginConfig": null,
				"Config": {
					"args": [
						"-c",
						"while true; do echo 'hello $(date)'; sleep 5; done",
					],
					"command": "sh",
					"image": "busybox:1.34.1",
				},
				"Constraints": null,
				"DispatchPayload": null,
				"Driver": "docker",
				"Env": null,
				"Identity": null,
				"KillSignal": "",
				"KillTimeout": 5000000000,
				"Kind": "",
				"Leader": false,
				"Lifecycle": null,
				"LogConfig": {
					"MaxFileSizeMB": 10,
					"MaxFiles": 10,
				},
				"Meta": {"postgres": "native"},
				"Name": "app",
				"Resources": {
					"CPU": 100,
					"Cores": 0,
					"Devices": null,
					"DiskMB": 0,
					"IOPS": 0,
					"MemoryMB": 300,
					"MemoryMaxMB": 0,
					"Networks": null,
				},
				"RestartPolicy": {
					"Attempts": 2,
					"Delay": 15000000000,
					"Interval": 1800000000000,
					"Mode": "fail",
				},
				"ScalingPolicies": null,
				"Services": null,
				"ShutdownDelay": 0,
				"Templates": null,
				"User": "",
				"Vault": null,
				"VolumeMounts": null,
			}],
			"Update": {
				"AutoPromote": false,
				"AutoRevert": false,
				"Canary": 0,
				"HealthCheck": "checks",
				"HealthyDeadline": 300000000000,
				"MaxParallel": 1,
				"MinHealthyTime": 10000000000,
				"ProgressDeadline": 600000000000,
				"Stagger": 30000000000,
			},
			"Volumes": null,
		}],
		"Type": "service",
		"Update": {
			"AutoPromote": false,
			"AutoRevert": false,
			"Canary": 0,
			"HealthCheck": "",
			"HealthyDeadline": 0,
			"MaxParallel": 1,
			"MinHealthyTime": 0,
			"ProgressDeadline": 0,
			"Stagger": 30000000000,
		},
		"VaultNamespace": "",
		"VaultToken": "",
		"Version": 6,
	}
}