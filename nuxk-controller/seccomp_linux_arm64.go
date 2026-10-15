//go:build linux && arm64

package main

const (
	auditArch  = 0xc00000b7 // AUDIT_ARCH_AARCH64
	sysSeccomp = 277
	x32Bit     = 0 // no x32 ABI here; 32-bit calls carry another arch
)
