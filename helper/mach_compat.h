#ifndef HELPER_MACH_COMPAT_H
#define HELPER_MACH_COMPAT_H

// iOS SDK redirects <mach/mach_vm.h> to #error. Hand-declare the
// primitives we actually use; they exist at runtime as MIG-generated
// trap wrappers in libsystem_kernel.

#include <mach/mach.h>
#include <mach/mach_types.h>
#include <mach/vm_map.h>
#include <mach/vm_region.h>
#include <sys/types.h>

typedef uint64_t mach_vm_address_t;
typedef uint64_t mach_vm_size_t;

extern kern_return_t mach_vm_read_overwrite(vm_map_t, mach_vm_address_t,
    mach_vm_size_t, mach_vm_address_t, mach_vm_size_t *);
extern kern_return_t mach_vm_region(vm_map_t, mach_vm_address_t *,
    mach_vm_size_t *, vm_region_flavor_t, vm_region_info_t,
    mach_msg_type_number_t *, mach_port_t *);
extern kern_return_t mach_vm_allocate(vm_map_t, mach_vm_address_t *,
    mach_vm_size_t, int);
extern kern_return_t mach_vm_deallocate(vm_map_t, mach_vm_address_t,
    mach_vm_size_t);
extern kern_return_t mach_vm_write(vm_map_t, mach_vm_address_t,
    vm_offset_t, mach_msg_type_number_t);
extern kern_return_t mach_vm_protect(vm_map_t, mach_vm_address_t,
    mach_vm_size_t, boolean_t, vm_prot_t);

// libproc + ptrace are also hidden by the iOS SDK.
extern int proc_listallpids(void *, int);
extern int proc_pidpath(int, void *, uint32_t);

#ifndef PT_TRACE_ME
#define PT_TRACE_ME 0
#endif
#ifndef PT_CONTINUE
#define PT_CONTINUE 7
#endif
extern int ptrace(int request, pid_t pid, void *addr, int data);

#ifndef VM_PROT_COPY
#define VM_PROT_COPY 0x10
#endif

#ifndef LC_ENCRYPTION_INFO
#define LC_ENCRYPTION_INFO 0x21
#endif
#ifndef LC_ENCRYPTION_INFO_64
#define LC_ENCRYPTION_INFO_64 0x2C
#endif

#ifndef FAT_MAGIC_64
#define FAT_MAGIC_64 0xcafebabf
#endif
#ifndef FAT_CIGAM_64
#define FAT_CIGAM_64 0xbfbafeca
#endif

#ifndef CPU_SUBTYPE_MASK
#define CPU_SUBTYPE_MASK 0xff000000
#endif

#endif // HELPER_MACH_COMPAT_H
