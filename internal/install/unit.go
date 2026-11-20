package install

// Unit devuelve la unit de systemd de Qaphi. Es la única fuente: el
// instalador la escribe y deploy/qaphi.service es una copia de referencia
// (un test comprueba que coinciden).
func Unit() string {
	return `# Generado por «qaphi install». Se regenera en cada actualización.
[Unit]
Description=Qaphi, monitor de servicios
After=network-online.target time-sync.target
Wants=network-online.target

[Service]
Type=simple
User=` + UserName + `
Group=` + UserName + `
EnvironmentFile=` + EnvPath + `
ExecStart=` + BinPath + ` serve
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s

# Datos en /var/lib/qaphi, solo accesibles por el usuario del servicio.
StateDirectory=qaphi
StateDirectoryMode=0700
ReadWritePaths=` + DataDir + `
UMask=0077

# Ping ICMP sin más privilegios que este.
AmbientCapabilities=CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_RAW

# Endurecimiento.
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
`
}
