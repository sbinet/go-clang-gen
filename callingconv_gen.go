package phoenix

// #include "go-clang.h"
import "C"

// Describes the calling convention of a function type
type CallingConv int

const (
	CallingConv_Default      CallingConv = C.CXCallingConv_Default
	CallingConv_C                        = C.CXCallingConv_C
	CallingConv_X86StdCall               = C.CXCallingConv_X86StdCall
	CallingConv_X86FastCall              = C.CXCallingConv_X86FastCall
	CallingConv_X86ThisCall              = C.CXCallingConv_X86ThisCall
	CallingConv_X86Pascal                = C.CXCallingConv_X86Pascal
	CallingConv_AAPCS                    = C.CXCallingConv_AAPCS
	CallingConv_AAPCS_VFP                = C.CXCallingConv_AAPCS_VFP
	CallingConv_PnaclCall                = C.CXCallingConv_PnaclCall
	CallingConv_IntelOclBicc             = C.CXCallingConv_IntelOclBicc
	CallingConv_X86_64Win64              = C.CXCallingConv_X86_64Win64
	CallingConv_X86_64SysV               = C.CXCallingConv_X86_64SysV
	CallingConv_Invalid                  = C.CXCallingConv_Invalid
	CallingConv_Unexposed                = C.CXCallingConv_Unexposed
)