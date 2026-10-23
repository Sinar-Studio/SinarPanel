//go:build cgo && (linux || darwin)

package main

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <stdlib.h>
#include <string.h>

#ifndef PAM_MAX_NUM_MSG
#define PAM_MAX_NUM_MSG 32
#endif

static void sp_wipe(char *s) {
	if (!s) return;
	volatile char *p = s;
	while (*p) *p++ = 0;
}

// Conversation function murni C: password dikirim lewat appdata_ptr sehingga
// tidak perlu callback ke Go. Hanya prompt "echo off" (password) yang dijawab.
static int sp_conv(int n, const struct pam_message **msg, struct pam_response **resp, void *appdata) {
	if (n <= 0 || n > PAM_MAX_NUM_MSG || appdata == NULL) return PAM_CONV_ERR;
	struct pam_response *r = calloc((size_t)n, sizeof(struct pam_response));
	if (!r) return PAM_BUF_ERR;
	for (int i = 0; i < n; i++) {
		switch (msg[i]->msg_style) {
		case PAM_PROMPT_ECHO_OFF:
			r[i].resp = strdup((const char *)appdata);
			if (!r[i].resp) goto fail;
			break;
		case PAM_ERROR_MSG:
		case PAM_TEXT_INFO:
			break;
		default:
			goto fail;
		}
	}
	*resp = r;
	return PAM_SUCCESS;
fail:
	for (int i = 0; i < n; i++) {
		if (r[i].resp) { sp_wipe(r[i].resp); free(r[i].resp); }
	}
	free(r);
	return PAM_CONV_ERR;
}

static int sp_auth(const char *service, const char *user, char *pass, const char *rhost, int *stage) {
	struct pam_conv *conv = malloc(sizeof(struct pam_conv));
	if (!conv) return PAM_BUF_ERR;
	conv->conv = sp_conv;
	conv->appdata_ptr = pass;
	pam_handle_t *h = NULL;
	*stage = 0;
	int rc = pam_start(service, user, conv, &h);
	if (rc != PAM_SUCCESS) { free(conv); return rc; }
	if (rhost && *rhost) pam_set_item(h, PAM_RHOST, rhost);
	*stage = 1;
	rc = pam_authenticate(h, PAM_DISALLOW_NULL_AUTHTOK);
	if (rc == PAM_SUCCESS) {
		*stage = 2;
		rc = pam_acct_mgmt(h, PAM_DISALLOW_NULL_AUTHTOK);
	}
	pam_end(h, rc);
	free(conv);
	return rc;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

var errPasswordExpired = errors.New("password user sudah kedaluwarsa, ganti dulu lewat SSH (passwd)")

func pamAuthenticate(service, user, password, rhost string) error {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cUser := C.CString(user)
	defer C.free(unsafe.Pointer(cUser))
	cRhost := C.CString(rhost)
	defer C.free(unsafe.Pointer(cRhost))
	cPass := C.CString(password)
	defer func() {
		C.sp_wipe(cPass)
		C.free(unsafe.Pointer(cPass))
	}()

	var stage C.int
	rc := C.sp_auth(cService, cUser, cPass, cRhost, &stage)
	if rc == C.PAM_SUCCESS {
		return nil
	}
	if stage == 2 && rc == C.PAM_NEW_AUTHTOK_REQD {
		return errPasswordExpired
	}
	return errors.New("pam: autentikasi gagal")
}
