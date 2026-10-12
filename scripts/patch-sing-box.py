"""Two compatibility fixes for the exact pinned sing-box release. Protocols are unchanged."""
import pathlib,sys
root=pathlib.Path(sys.argv[1])
p=root/"route/network.go"
s=p.read_text()
if "aria: optional monitor" not in s:
    anchor="enforceInterfaceMonitor := options.AutoDetectInterface"
    assert anchor in s
    s=s.replace("type NetworkManager struct {","type NetworkManager struct {\n\tariaEnforceMonitor bool",1)
    s=s.replace(anchor,anchor+"\n\tnm.ariaEnforceMonitor = enforceInterfaceMonitor",1)
    anchor="\t\t\tif err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tscope.Add(r.networkMonitor.Close)"
    assert anchor in s
    replacement="""            if err != nil {
                // aria: optional monitor. Explicit auto-detection remains mandatory.
                if !r.ariaEnforceMonitor && errors.Is(err, os.ErrPermission) {
                    _ = r.networkMonitor.Close()
                    r.networkMonitor = nil
                    r.interfaceMonitor = nil
                    r.logger.Warn("optional system network monitor unavailable; using system routing")
                } else { return err }
            }
            if r.networkMonitor != nil { scope.Add(r.networkMonitor.Close) }"""
    s=s.replace(anchor,replacement,1)
    p.chmod(0o644);p.write_text(s)
p=root/"experimental/v2rayapi/server.go"
s=p.read_text()
if "aria: Xray stats alias" not in s:
    anchor="RegisterStatsServiceServer(grpcServer, statsService)"
    assert anchor in s
    replacement=anchor+"""
        // aria: Xray stats alias; identical protobuf payloads.
        xrayDescriptor := StatsService_ServiceDesc
        xrayDescriptor.ServiceName = "xray.app.stats.command.StatsService"
        grpcServer.RegisterService(&xrayDescriptor, statsService)"""
    s=s.replace(anchor,replacement,1)
    p.chmod(0o644);p.write_text(s)
print("Pinned sing-box compatibility patches ready")
