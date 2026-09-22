@echo off
rem Windows shim: lets `go run` on a dev box exec the sh mock via Git Bash.
rem Not shipped — testdata only. On the router S52xray is a real init script.
"%PROGRAMFILES%\Git\bin\bash.exe" "%~dp0S52xray-mock" %*
