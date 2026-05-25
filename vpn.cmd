@echo off
rem Thin shim: the real work happens in vpn.exe (Go controller).
rem Kept for backwards-compat with anything that still types `vpn.cmd`.
"%~dp0vpn.exe" %*
