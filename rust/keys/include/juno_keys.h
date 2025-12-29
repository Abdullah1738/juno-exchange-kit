#pragma once

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Returns JSON:
//  {"status":"ok","ufvk":"jview..."} or {"status":"err","error":"..."}
char *juno_keys_ufvk_from_seed_json(const char *req_json);

// Returns JSON:
//  {"status":"ok","address":"j..."} or {"status":"err","error":"..."}
char *juno_keys_address_from_ufvk_json(const char *req_json);

void juno_keys_string_free(char *s);

#ifdef __cplusplus
} // extern "C"
#endif

