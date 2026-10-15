//go:build linux && amd64

package main

const (
	auditArch  = 0xc000003e // AUDIT_ARCH_X86_64
	sysSeccomp = 317
	x32Bit     = 0x40000000 // __X32_SYSCALL_BIT: the x32 ABI, same arch, other numbers
)
