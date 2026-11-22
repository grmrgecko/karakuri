/*
 * karakuri-unlock.cpp — the karakuri credential provider.
 *
 * Windows dismisses a lock screen only for a credential handed in on the
 * secure desktop itself, and the sanctioned way to feed winlogon one is a
 * credential provider: a COM DLL LogonUI loads on WinSta0. This one hosts a
 * local named pipe; the karakuri agent dials it and queues one credential —
 * the same envelope store the typed unlock resolves — and when one is
 * armed, the provider enumerates an auto-logon credential that winlogon
 * submits with no keyboard entry at all, the same wedge Windows Hello
 * companion devices use. With nothing armed the provider reports no tiles
 * and the logon screen stays exactly the machine's default.
 *
 * The provider is a V2 credential provider: LogonUI hands it the session's
 * ICredentialProviderUserArray, and an armed credential is bound to the
 * matching user from that array — SID included through
 * ICredentialProviderCredential2::GetUserSid — which is what makes the tile
 * "for" the locked user rather than a generic one.
 *
 * The serialization follows the sample contract exactly: the strings ride
 * the end of the KERB_INTERACTIVE_UNLOCK_LOGON buffer as byte offsets and
 * are NOT NUL-terminated, the password is protected with CredProtectW (lsass
 * unprotects on its side), and the LUID is zero — winlogon fills it in.
 * KERB_INTERACTIVE_UNLOCK_LOGON serves both scenarios (the message type
 * selects KerbWorkstationUnlockLogon for the lock screen,
 * KerbInteractiveLogon for the sign-in screen).
 *
 * The pipe is one per session — \\.\pipe\karakuri-unlock-s<sessionid> —
 * created here, since LogonUI (which hosts this DLL) lives exactly as long
 * as the screen it is showing. Frames are little-endian u32s with UTF-16
 * strings length-prefixed in bytes; no JSON, so no escaping of password
 * characters. The agent connects, sends one arm frame, and waits for the
 * result frame this side answers once winlogon has reported.
 *
 * Built with the mingw-w64 cross toolchain, 64-bit — LogonUI is.
 */

#include <initguid.h>
#include <windows.h>
#include <wincred.h>
#include <ntsecapi.h>
#include <wtsapi32.h>
#include <credentialprovider.h>
#include <propkey.h>
#include <strsafe.h>
#include <sddl.h>

// {7E1A0C42-8D3B-4F5A-9C3D-6E10A3F4E5B6}
static const GUID CLSID_KarakuriUnlock = {
    0x7e1a0c42, 0x8d3b, 0x4f5a, {0x9c, 0x3d, 0x6e, 0x10, 0xa3, 0xf4, 0xe5, 0xb6}};

#ifndef NT_SUCCESS
#define NT_SUCCESS(Status) ((NTSTATUS)(Status) >= 0)
#endif

static HINSTANCE g_hModule;
static long g_objects;   // provider/credential objects alive

// Frame constants shared with the Go side.
static const ULONG frameMagic = 0x4b554e4c;  // "KUNL"
static const ULONG frameVersion = 1;

// sessionLocked asks Terminal Services whether the session this provider is
// hosted in is locked. The answer decides the logon type: a locked session
// takes KerbWorkstationUnlockLogon even when LogonUI reports the surface as
// a plain logon (Win11's lock screen does).
static bool sessionLocked()
{
	DWORD sessionId = 0;
	ProcessIdToSessionId(GetCurrentProcessId(), &sessionId);
	LPWSTR buf = nullptr;
	DWORD bytes = 0;
	if (!WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, sessionId,
		WTSSessionInfoEx, &buf, &bytes) || !buf) {
		return false;
	}
	// WTSINFOEXW: DWORD Level, then WTSINFOEX_LEVEL1 whose first members are
	// SessionId, SessionState, SessionFlags — 0 = locked, 1 = unlocked.
	typedef struct { ULONG Level; ULONG SessionId; ULONG SessionState; ULONG SessionFlags; } LOCKVIEW;
	bool locked = false;
	if (bytes >= sizeof(LOCKVIEW)) {
		LOCKVIEW *view = (LOCKVIEW *)buf;
		locked = (view->SessionFlags == 0);
	}
	WTSFreeMemory(buf);
	return locked;
}

static void writeU32(BYTE *p, ULONG v)
{
	p[0] = (BYTE)v; p[1] = (BYTE)(v >> 8); p[2] = (BYTE)(v >> 16); p[3] = (BYTE)(v >> 24);
}

static ULONG readU32(const BYTE *p)
{
	return (ULONG)p[0] | ((ULONG)p[1] << 8) | ((ULONG)p[2] << 16) | ((ULONG)p[3] << 24);
}

// ---------------------------------------------------------------- utilities

static void zeroFree(wchar_t **p)
{
	if (*p) {
		SecureZeroMemory(*p, (lstrlenW(*p) + 1) * sizeof(WCHAR));
		CoTaskMemFree(*p);
		*p = nullptr;
	}
}

// strCopy duplicates one wide string into a CoTaskMem buffer, which is what
// every out-parameter string in this interface set is allocated with.
static HRESULT strCopy(const wchar_t *text, wchar_t **out)
{
	SIZE_T bytes = (lstrlenW(text) + 1) * sizeof(WCHAR);
	wchar_t *copy = (wchar_t *)CoTaskMemAlloc(bytes);
	if (!copy) return E_OUTOFMEMORY;
	memcpy(copy, text, bytes);
	*out = copy;
	return S_OK;
}

// protectAndCopyString encrypts the password with CredProtect, the exact
// protection the logon path expects — lsass unprotects on its side. The
// first call sizes the buffer, the second encrypts; the character count
// includes the NUL.
static HRESULT protectAndCopyString(const wchar_t *password, wchar_t **out)
{
	*out = nullptr;
	if (!password || !*password) return strCopy(password ? password : L"", out);
	wchar_t *plain = nullptr;
	HRESULT hr = strCopy(password, &plain);
	if (FAILED(hr)) return hr;
	DWORD needed = 0;
	if (!CredProtectW(FALSE, plain, (DWORD)lstrlenW(plain) + 1, nullptr, &needed, nullptr) &&
		GetLastError() == ERROR_INSUFFICIENT_BUFFER && needed > 0) {
		wchar_t *protected_ = (wchar_t *)CoTaskMemAlloc(needed * sizeof(WCHAR));
		if (protected_ &&
			CredProtectW(FALSE, plain, (DWORD)lstrlenW(plain) + 1, protected_, &needed, nullptr)) {
			*out = protected_;
		} else {
			SecureZeroMemory(plain, (lstrlenW(plain) + 1) * sizeof(WCHAR));
			hr = FAILED(hr) ? hr : E_FAIL;
		}
	} else {
		hr = FAILED(hr) ? hr : E_FAIL;
	}
	SecureZeroMemory(plain, (lstrlenW(plain) + 1) * sizeof(WCHAR));
	CoTaskMemFree(plain);
	return hr;
}

// unicodeStringInit aims a UNICODE_STRING at a string the caller owns, the
// weak-reference form the packing pass consumes.
static void unicodeStringInit(const wchar_t *text, UNICODE_STRING *out)
{
	ULONG bytes = (ULONG)lstrlenW(text) * sizeof(WCHAR);
	out->Length = (USHORT)bytes;
	out->MaximumLength = (USHORT)bytes;
	out->Buffer = const_cast<wchar_t *>(text);
}

// packKerberos serializes the queued credential the way Winlogon and LSA
// consume it: struct first, then the three strings, with each UNICODE_STRING
// Buffer an offset into the blob and no NUL terminators. The LUID stays
// zero — Winlogon fills in the logon session it is unlocking.

// resolveNegotiatePackage asks LSA which authentication package the negotiate
// provider is, which is the blob's address.
static ULONG resolveNegotiatePackage()
{
	HANDLE lsa;
	if (!NT_SUCCESS(LsaConnectUntrusted(&lsa))) return 0;
	ULONG package = 0;
	LSA_STRING name;
	static char value[] = "Negotiate";
	name.Buffer = value;
	name.Length = (USHORT)(sizeof("negotiate") - 1);
	name.MaximumLength = (USHORT)sizeof("negotiate");
	NTSTATUS st = LsaLookupAuthenticationPackage(lsa, &name, &package);
	LsaDeregisterLogonProcess(lsa);
	if (!NT_SUCCESS(st)) return 0;
	return package;
}

// securityAttributes restricts the pipe to SYSTEM and Administrators, which
// is who the agent is expected to be.
static SECURITY_ATTRIBUTES *securityAttributes()
{
	static PSECURITY_DESCRIPTOR sd;
	if (!sd) {
		if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(
			L"D:P(A;;GA;;;SY)(A;;GA;;;BA)", SDDL_REVISION_1, &sd, nullptr)) {
			return nullptr;
		}
	}
	static SECURITY_ATTRIBUTES sa;
	sa.nLength = sizeof(sa);
	sa.lpSecurityDescriptor = sd;
	sa.bInheritHandle = FALSE;
	return &sa;
}

static bool readFully(HANDLE pipe, BYTE *buf, ULONG size)
{
	ULONG done = 0;
	while (done < size) {
		ULONG read = 0;
		if (!ReadFile(pipe, buf + done, size - done, &read, nullptr) || !read) return false;
		done += read;
	}
	return true;
}

static bool writeFully(HANDLE pipe, const BYTE *buf, ULONG size)
{
	ULONG done = 0;
	while (done < size) {
		ULONG written = 0;
		if (!WriteFile(pipe, buf + done, size - done, &written, nullptr) || !written) return false;
		done += written;
	}
	return true;
}

// readPayload reads one length-prefixed UTF-16 string out of the frame.
static bool readPayload(const BYTE *buf, ULONG size, ULONG *pos, wchar_t **out)
{
	if (*pos + 4 > size) return false;
	ULONG bytes = readU32(buf + *pos);
	*pos += 4;
	if (bytes & 1 || *pos + bytes > size) return false;
	wchar_t *s = (wchar_t *)CoTaskMemAlloc((SIZE_T)bytes + sizeof(WCHAR));
	if (!s) return false;
	memcpy(s, buf + *pos, bytes);
	s[bytes / sizeof(WCHAR)] = 0;
	*pos += bytes;
	*out = s;
	return true;
}

// The usage scenario this provider instance is hosted in, the negotiate
// package id, and the user array LogonUI handed over.
static CREDENTIAL_PROVIDER_USAGE_SCENARIO g_usage = CPUS_INVALID;
static ULONG g_authPackage;
static ICredentialProviderUserArray *g_userArray;
static ICredentialProviderEvents *g_events;  // LogonUI's re-enumeration signal
static UINT_PTR g_adviseContext;
static HANDLE g_firstPull;   // set after the broker's first exchange with the agent

// ---------------------------------------------------------------- queue

// The credential the agent queued: the strings plus the user from the
// session's array that the strings belong to. One at a time.
struct QueuedCredential {
	wchar_t *username;      // the bare account name as queued
	wchar_t *password;
	ICredentialProviderUser *user;  // the array entry bound to the account; may be null
	bool armed;
	bool resultSet;    // ReportResult has answered
	bool consumed;     // GetSerialization packed the credential
	ULONG resultStatus;
};
static QueuedCredential g_queued;
static CRITICAL_SECTION g_credLock;
static HANDLE g_resultEvent;    // set by ReportResult

// matchUser finds the session-array entry the queued account belongs to: the
// account part of the qualified name, the bare user name, or the whole
// queued string against either name the array carries. The answer is
// AddRef'd; null when no entry matches.
static ICredentialProviderUser *matchUser(const wchar_t *username)
{
	if (!g_userArray) return nullptr;
	DWORD count = 0;
	if (FAILED(g_userArray->GetCount(&count))) return nullptr;
	for (DWORD i = 0; i < count; i++) {
		ICredentialProviderUser *user = nullptr;
		if (FAILED(g_userArray->GetAt(i, &user))) continue;
		LPWSTR qualified = nullptr;
		LPWSTR name = nullptr;
		user->GetStringValue(PKEY_Identity_QualifiedUserName, &qualified);
		user->GetStringValue(PKEY_Identity_UserName, &name);
		bool match = false;
		const wchar_t *account = username;
		for (const wchar_t *p = username; *p; p++) {
			if (*p == L'\\') account = p + 1;
		}
		if (qualified && lstrcmpiW(qualified, username) == 0) match = true;
		if (!match && name && lstrcmpiW(name, account) == 0) match = true;
		if (!match && qualified) {
			const wchar_t *qAccount = qualified;
			for (const wchar_t *p = qualified; *p; p++) {
				if (*p == L'\\') qAccount = p + 1;
			}
			if (lstrcmpiW(qAccount, account) == 0) match = true;
		}
		CoTaskMemFree(qualified);
		CoTaskMemFree(name);
		if (match) return user;
		user->Release();
	}
	return nullptr;
}

static void clearQueued()
{
	zeroFree(&g_queued.username);
	zeroFree(&g_queued.password);
	if (g_queued.user) {
		g_queued.user->Release();
		g_queued.user = nullptr;
	}
	g_queued.armed = false;
}

// --------------------------------------------------------------- credential

// The one tile: a quiet label plus its submit button. The strings the tile
// displays come from the queued credential; the field values themselves are
// never read back — GetSerialization packs from the queue directly.
static const DWORD FIELD_LABEL = 0;
static const DWORD FIELD_SUBMIT = 1;
static const DWORD FIELD_COUNT = 2;
static const CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR s_fields[] = {
	{ FIELD_LABEL, CPFT_LARGE_TEXT, nullptr, {0} },
	{ FIELD_SUBMIT, CPFT_SUBMIT_BUTTON, nullptr, {0} },
};

// splitQualified divides DOMAIN\user into its parts; a bare name keeps an
// empty domain, which the local machine resolves.
static HRESULT splitQualified(const wchar_t *qualified, wchar_t **domain, wchar_t **user)
{
	const wchar_t *slash = nullptr;
	for (const wchar_t *p = qualified; *p; p++) {
		if (*p == L'\\') slash = p;
	}
	if (!slash) {
		HRESULT hr = strCopy(L"", domain);
		if (SUCCEEDED(hr)) hr = strCopy(qualified, user);
		return hr;
	}
	SIZE_T domainChars = slash - qualified;
	SIZE_T domainBytes = domainChars * sizeof(WCHAR) + sizeof(WCHAR);
	*domain = (wchar_t *)CoTaskMemAlloc(domainBytes);
	if (!*domain) return E_OUTOFMEMORY;
	memcpy(*domain, qualified, domainChars * sizeof(WCHAR));
	(*domain)[domainChars] = 0;
	return strCopy(slash + 1, user);
}

class CKarakuriCredential : public ICredentialProviderCredential2,
	public ICredentialProviderCredentialWithFieldOptions {
public:
	CKarakuriCredential() : refs_(1), pcpe_(nullptr) { InterlockedIncrement(&g_objects); }
	virtual ~CKarakuriCredential() {
		zeroFree(&sid_);
		zeroFree(&qualified_);
		zeroFree(&password_);
		InterlockedDecrement(&g_objects);
	}

	// Initialize binds the credential to a user from the session's array and
	// the queued credential. An empty qualified name means no array entry
	// matched; the domain falls back to the local machine.
	HRESULT Initialize(CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus,
		ICredentialProviderUser *user, const wchar_t *username, const wchar_t *password)
	{
		usage_ = cpus;
		HRESULT hr = strCopy(username, &username_);
		if (FAILED(hr)) return hr;
		hr = strCopy(password, &password_);
		if (FAILED(hr)) return hr;
		if (user) {
			user->GetSid(&sid_);          // null for an account with no SID
			user->GetStringValue(PKEY_Identity_QualifiedUserName, &qualified_);
		}
		if (!qualified_) {
			wchar_t machine[MAX_COMPUTERNAME_LENGTH + 1];
			DWORD size = MAX_COMPUTERNAME_LENGTH + 1;
			if (!GetComputerNameW(machine, &size)) StringCchCopyW(machine, ARRAYSIZE(machine), L".");
			SIZE_T bytes = (lstrlenW(machine) + 1 + lstrlenW(username) + 1) * sizeof(WCHAR);
			qualified_ = (wchar_t *)CoTaskMemAlloc(bytes);
			if (!qualified_) return E_OUTOFMEMORY;
			StringCchPrintfW(qualified_, bytes, L"%s\\%s", machine, username);
		}
		return S_OK;
	}

	// IUnknown
	HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void **out) override {
		if (riid == IID_IUnknown || riid == IID_ICredentialProviderCredential) {
			*out = static_cast<ICredentialProviderCredential *>(this); AddRef();
			return S_OK;
		}
		if (riid == IID_ICredentialProviderCredential2) {
			*out = static_cast<ICredentialProviderCredential2 *>(this); AddRef();
			return S_OK;
		}
		if (riid == IID_ICredentialProviderCredentialWithFieldOptions) {
			*out = static_cast<ICredentialProviderCredentialWithFieldOptions *>(this); AddRef();
			return S_OK;
		}
		*out = nullptr; return E_NOINTERFACE;
	}
	ULONG STDMETHODCALLTYPE AddRef(void) override { return InterlockedIncrement(&refs_); }
	ULONG STDMETHODCALLTYPE Release(void) override {
		ULONG left = InterlockedDecrement(&refs_);
		if (!left) delete this;
		return left;
	}

	// ICredentialProviderCredential
	HRESULT STDMETHODCALLTYPE Advise(ICredentialProviderCredentialEvents *pcpce) override {
		if (pcpe_) pcpe_->Release();
		pcpe_ = pcpce; if (pcpe_) pcpe_->AddRef();
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE UnAdvise(void) override {
		if (pcpe_) { pcpe_->Release(); pcpe_ = nullptr; }
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE SetSelected(WINBOOL *pbAutoLogon) override {
			// The wedge: an armed credential asks for automatic submit, so
		// winlogon dismisses the lock (or signs the user in) with no
		// keyboard entry at all.
		EnterCriticalSection(&g_credLock);
		bool armed = g_queued.armed;
		LeaveCriticalSection(&g_credLock);
		if (pbAutoLogon) *pbAutoLogon = armed ? TRUE : FALSE;
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE SetDeselected(void) override {
		EnterCriticalSection(&g_credLock);
		g_queued.armed = false;
		LeaveCriticalSection(&g_credLock);
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetFieldState(DWORD dwFieldID,
		CREDENTIAL_PROVIDER_FIELD_STATE *pcpfs,
		CREDENTIAL_PROVIDER_FIELD_INTERACTIVE_STATE *pcpfis) override {
		if (dwFieldID >= FIELD_COUNT || !pcpfs || !pcpfis) return E_INVALIDARG;
		*pcpfs = CPFS_DISPLAY_IN_SELECTED_TILE;
		*pcpfis = CPFIS_NONE;
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetStringValue(DWORD dwFieldID, LPWSTR *ppsz) override {
		if (!ppsz) return E_INVALIDARG;
		if (dwFieldID != FIELD_LABEL) return E_NOTIMPL;
		return strCopy(L"Karakuri Unlock", ppsz);
	}
	HRESULT STDMETHODCALLTYPE GetBitmapValue(DWORD, HBITMAP *) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE GetCheckboxValue(DWORD, WINBOOL *, LPWSTR *) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE GetSubmitButtonValue(DWORD dwFieldID, DWORD *pdwAdjacentTo) override {
		// The tile declares a submit button, so LogonUI asks where it goes;
		// without an adjacency the tile cannot be submitted at all.
		if (dwFieldID != FIELD_SUBMIT || !pdwAdjacentTo) return E_INVALIDARG;
		*pdwAdjacentTo = FIELD_LABEL;
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetComboBoxValueCount(DWORD, DWORD *, DWORD *) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE GetComboBoxValueAt(DWORD, DWORD, LPWSTR *) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE SetStringValue(DWORD, LPCWSTR) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE SetCheckboxValue(DWORD, WINBOOL) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE SetComboBoxSelectedValue(DWORD, DWORD) override { return E_NOTIMPL; }
	HRESULT STDMETHODCALLTYPE CommandLinkClicked(DWORD) override { return E_NOTIMPL; }

	HRESULT STDMETHODCALLTYPE GetSerialization(
		CREDENTIAL_PROVIDER_GET_SERIALIZATION_RESPONSE *pcpgsr,
		CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION *pcpcs,
		LPWSTR *ppszOptionalStatusText,
		CREDENTIAL_PROVIDER_STATUS_ICON *pcpsiOptionalStatusIcon) override {
		EnterCriticalSection(&g_credLock);
		bool armed = g_queued.armed;
		LeaveCriticalSection(&g_credLock);
		if (!armed || !username_ || !password_) {
			*pcpgsr = CPGSR_NO_CREDENTIAL_NOT_FINISHED;
			return S_OK;
		}

		// The qualified name splits into domain and account; the password
		// rides CredProtect-encrypted and the LUID is winlogon's to fill.
		wchar_t *domain = nullptr, *user = nullptr;
		HRESULT hr = splitQualified(qualified_, &domain, &user);
		HRESULT hr2 = hr;
		wchar_t *protectedPassword = nullptr;
		if (SUCCEEDED(hr2)) hr2 = protectAndCopyString(password_, &protectedPassword);
		if (SUCCEEDED(hr2)) {
			KERB_INTERACTIVE_UNLOCK_LOGON kiul;
			ZeroMemory(&kiul, sizeof(kiul));
			// The logon type follows the usage scenario, as the sample
			// does: KERB_INTERACTIVE_UNLOCK_LOGON carries both message
			// types, and winlogon's surface decides which one applies.
			kiul.Logon.MessageType = (usage_ == CPUS_UNLOCK_WORKSTATION)
				? KerbWorkstationUnlockLogon : KerbInteractiveLogon;
			unicodeStringInit(domain, &kiul.Logon.LogonDomainName);
			unicodeStringInit(user, &kiul.Logon.UserName);
			unicodeStringInit(protectedPassword, &kiul.Logon.Password);

			ULONG blobSize = sizeof(kiul) + kiul.Logon.LogonDomainName.Length +
				kiul.Logon.UserName.Length + kiul.Logon.Password.Length;
			BYTE *blob = (BYTE *)CoTaskMemAlloc(blobSize);
			if (blob) {
				ZeroMemory(blob, blobSize);
				KERB_INTERACTIVE_LOGON *logon = (KERB_INTERACTIVE_LOGON *)blob;
				logon->MessageType = kiul.Logon.MessageType;
				// The lengths ride with the struct — the sample's
				// _UnicodeStringPackedUnicodeStringCopy copies them too;
				// leaving them zeroed hands LSA three empty strings.
				logon->LogonDomainName = kiul.Logon.LogonDomainName;
				logon->UserName = kiul.Logon.UserName;
				logon->Password = kiul.Logon.Password;
				BYTE *tail = blob + sizeof(kiul);
				memcpy(tail, domain, kiul.Logon.LogonDomainName.Length);
				logon->LogonDomainName.Buffer = (PWSTR)(tail - blob);
				tail += kiul.Logon.LogonDomainName.Length;
				memcpy(tail, user, kiul.Logon.UserName.Length);
				logon->UserName.Buffer = (PWSTR)(tail - blob);
				tail += kiul.Logon.UserName.Length;
				memcpy(tail, protectedPassword, kiul.Logon.Password.Length);
				logon->Password.Buffer = (PWSTR)(tail - blob);

				EnterCriticalSection(&g_credLock);
				g_queued.consumed = true;
				LeaveCriticalSection(&g_credLock);
				*pcpgsr = CPGSR_RETURN_CREDENTIAL_FINISHED;
				pcpcs->ulAuthenticationPackage = g_authPackage;
				pcpcs->clsidCredentialProvider = CLSID_KarakuriUnlock;
				pcpcs->cbSerialization = blobSize;
				pcpcs->rgbSerialization = blob;
			} else {
				hr2 = E_OUTOFMEMORY;
			}
		}
		zeroFree(&domain);
		zeroFree(&user);
		if (protectedPassword) {
			SecureZeroMemory(protectedPassword, (lstrlenW(protectedPassword) + 1) * sizeof(WCHAR));
			CoTaskMemFree(protectedPassword);
		}
		if (FAILED(hr2)) {
			*pcpgsr = CPGSR_NO_CREDENTIAL_NOT_FINISHED;
		}
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE ReportResult(NTSTATUS ntsStatus, NTSTATUS ntsSubstatus,
		LPWSTR *ppszOptionalStatusText,
		CREDENTIAL_PROVIDER_STATUS_ICON *pcpsiOptionalStatusIcon) override {
		// Winlogon's answer to the submitted credential: this is the result
		// the waiting agent reads.
		EnterCriticalSection(&g_credLock);
		g_queued.resultStatus = ntsStatus;
		g_queued.resultSet = true;
		LeaveCriticalSection(&g_credLock);
		SetEvent(g_resultEvent);
		return S_OK;
	}

	// ICredentialProviderCredential2
	HRESULT STDMETHODCALLTYPE GetUserSid(LPWSTR *sid) override {
		if (!sid) return E_INVALIDARG;
		*sid = nullptr;
		if (!sid_) return S_FALSE;   // an unbound credential: empty user tile
		return strCopy(sid_, sid);
	}

	// ICredentialProviderCredentialWithFieldOptions
	HRESULT STDMETHODCALLTYPE GetFieldOptions(DWORD, CREDENTIAL_PROVIDER_CREDENTIAL_FIELD_OPTIONS *out) override {
		if (!out) return E_INVALIDARG;
		*out = CPCFO_NONE;
		return S_OK;
	}

private:
	ULONG refs_;
	ICredentialProviderCredentialEvents *pcpe_;
	CREDENTIAL_PROVIDER_USAGE_SCENARIO usage_;
	wchar_t *sid_;          // the bound user's SID, or null
	wchar_t *qualified_;    // DOMAIN\user as the array names it
	wchar_t *username_;     // the queued account name
	wchar_t *password_;     // the queued secret
};

// ---------------------------------------------------------------- provider

class CKarakuriProvider : public ICredentialProvider, public ICredentialProviderSetUserArray {
public:
	CKarakuriProvider() : refs_(1), credential_(nullptr), events_(nullptr), adviseContext_(0) {
		InterlockedIncrement(&g_objects);
	}
	virtual ~CKarakuriProvider() {
		if (credential_) credential_->Release();
		if (events_) events_->Release();
		InterlockedDecrement(&g_objects);
	}

	HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void **out) override {
		if (riid == IID_IUnknown || riid == IID_ICredentialProvider) {
			*out = static_cast<ICredentialProvider *>(this); AddRef();
			return S_OK;
		}
		if (riid == IID_ICredentialProviderSetUserArray) {
			*out = static_cast<ICredentialProviderSetUserArray *>(this); AddRef();
			return S_OK;
		}
		*out = nullptr; return E_NOINTERFACE;
	}
	ULONG STDMETHODCALLTYPE AddRef(void) override { return InterlockedIncrement(&refs_); }
	ULONG STDMETHODCALLTYPE Release(void) override {
		ULONG left = InterlockedDecrement(&refs_);
		if (!left) delete this;
		return left;
	}

	HRESULT STDMETHODCALLTYPE SetUsageScenario(CREDENTIAL_PROVIDER_USAGE_SCENARIO cpus, DWORD dwFlags) override {
		switch (cpus) {
		case CPUS_LOGON:
		case CPUS_UNLOCK_WORKSTATION:
			EnterCriticalSection(&g_credLock);
			g_usage = cpus;
			LeaveCriticalSection(&g_credLock);
			recreate_ = true;
			return S_OK;
		default:
			return E_NOTIMPL;
		}
	}
	HRESULT STDMETHODCALLTYPE SetSerialization(const CREDENTIAL_PROVIDER_CREDENTIAL_SERIALIZATION *pcpcs) override {
		return E_NOTIMPL;
	}
	HRESULT STDMETHODCALLTYPE Advise(ICredentialProviderEvents *pcpe, UINT_PTR upAdviseContext) override {
		if (events_) events_->Release();
		events_ = pcpe; if (events_) events_->AddRef();
		adviseContext_ = upAdviseContext;
		// The broker thread signals a queued credential through the same
		// events object, which is what makes LogonUI re-enumerate the tiles
		// after it built its list.
		EnterCriticalSection(&g_credLock);
		if (g_events) g_events->Release();
		g_events = pcpe; if (g_events) g_events->AddRef();
		g_adviseContext = upAdviseContext;
		LeaveCriticalSection(&g_credLock);
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE UnAdvise(void) override {
		if (events_) { events_->Release(); events_ = nullptr; }
		EnterCriticalSection(&g_credLock);
		if (g_events) { g_events->Release(); g_events = nullptr; }
		LeaveCriticalSection(&g_credLock);
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetFieldDescriptorCount(DWORD *pdwCount) override {
		*pdwCount = FIELD_COUNT;
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetFieldDescriptorAt(DWORD dwIndex, CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR **ppcpfd) override {
		if (dwIndex >= FIELD_COUNT || !ppcpfd) return E_INVALIDARG;
		CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR *fd =
			(CREDENTIAL_PROVIDER_FIELD_DESCRIPTOR *)CoTaskMemAlloc(sizeof(**ppcpfd));
		if (!fd) return E_OUTOFMEMORY;
		fd->dwFieldID = s_fields[dwIndex].dwFieldID;
		fd->cpft = s_fields[dwIndex].cpft;
		fd->pszLabel = nullptr;
		fd->guidFieldType = {};
		*ppcpfd = fd;
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetCredentialCount(DWORD *pdwCount, DWORD *pdwDefault, WINBOOL *pbAutoLogonWithDefault) override {
		// The first enumeration waits for the broker's first exchange with
		// the agent, so a credential queued across a logon-screen restart is
		// behind this enumeration instead of racing it.
		WaitForSingleObject(g_firstPull, 2000);
		// Rebuild from the queue on every enumeration: the tile list is
		// cheap (one credential), and a credential armed after the screen
		// came up only becomes visible when the enumeration re-runs.
		if (credential_) { credential_->Release(); credential_ = nullptr; }
		credential_ = createCredential();
		recreate_ = false;
		if (credential_) {
			*pdwCount = 1;
			*pdwDefault = 0;
			*pbAutoLogonWithDefault = TRUE;
		} else {
			*pdwCount = 0;
			*pdwDefault = CREDENTIAL_PROVIDER_NO_DEFAULT;
			*pbAutoLogonWithDefault = FALSE;
		}
		return S_OK;
	}
	HRESULT STDMETHODCALLTYPE GetCredentialAt(DWORD dwIndex, ICredentialProviderCredential **ppcpc) override {
		if (dwIndex != 0 || !ppcpc || !credential_) return E_INVALIDARG;
			return credential_->QueryInterface(IID_ICredentialProviderCredential, (void **)ppcpc);
	}

	// ICredentialProviderSetUserArray
	HRESULT STDMETHODCALLTYPE SetUserArray(ICredentialProviderUserArray *users) override {
		EnterCriticalSection(&g_credLock);
		if (userArray_) userArray_->Release();
		userArray_ = users; if (userArray_) userArray_->AddRef();
		g_userArray = users; if (g_userArray) g_userArray->AddRef();
		LeaveCriticalSection(&g_credLock);
		recreate_ = true;
		return S_OK;
	}

private:
	// createCredential builds the tile for one queued credential: the user
	// from the session's array whose account matches, the queued strings,
	// and the current usage scenario. Null when nothing is armed.
	CKarakuriCredential *createCredential() {
		EnterCriticalSection(&g_credLock);
		if (!g_queued.armed || !g_queued.username || !g_queued.password) {
			LeaveCriticalSection(&g_credLock);
			return nullptr;
		}
		ULONG ul = (ULONG)(lstrlenW(g_queued.username) + 1) * sizeof(WCHAR);
		ULONG pw = (ULONG)(lstrlenW(g_queued.password) + 1) * sizeof(WCHAR);
		wchar_t *u = (wchar_t *)CoTaskMemAlloc(ul);
		wchar_t *p = (wchar_t *)CoTaskMemAlloc(pw);
		ICredentialProviderUser *user = g_queued.user;
		if (user) user->AddRef();
		if (u && p) {
			memcpy(u, g_queued.username, ul);
			memcpy(p, g_queued.password, pw);
		}
		LeaveCriticalSection(&g_credLock);
		if (!u || !p) {
			zeroFree(&u); zeroFree(&p);
			if (user) user->Release();
			return nullptr;
		}
		CKarakuriCredential *c = new CKarakuriCredential();
		if (FAILED(c->Initialize(g_usage, user, u, p))) {
			c->Release();
			c = nullptr;
		}
		zeroFree(&u); zeroFree(&p);
		if (user) user->Release();
		return c;
	}

	ULONG refs_;
	CKarakuriCredential *credential_;
	ICredentialProviderEvents *events_;
	UINT_PTR adviseContext_;
	ICredentialProviderUserArray *userArray_ = nullptr;
	bool recreate_ = false;
};

// ---------------------------------------------------------------- class factory

class CKarakuriClassFactory : public IClassFactory {
public:
	HRESULT STDMETHODCALLTYPE QueryInterface(REFIID riid, void **out) override {
		if (riid == IID_IUnknown || riid == IID_IClassFactory) {
			*out = static_cast<IClassFactory *>(this); AddRef(); return S_OK;
		}
		*out = nullptr; return E_NOINTERFACE;
	}
	ULONG STDMETHODCALLTYPE AddRef(void) override { return 2; }
	ULONG STDMETHODCALLTYPE Release(void) override { return 1; }
	HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown *outer, REFIID riid, void **out) override {
		*out = nullptr;
		if (outer) return CLASS_E_NOAGGREGATION;
		CKarakuriProvider *p = new CKarakuriProvider();
		HRESULT hr = p->QueryInterface(riid, out);
		p->Release();
		return hr;
	}
	HRESULT STDMETHODCALLTYPE LockServer(BOOL) override { return S_OK; }
};

// ---------------------------------------------------------------- COM entry

STDAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void **out) {
	if (out) *out = nullptr;
	if (!IsEqualCLSID(rclsid, CLSID_KarakuriUnlock)) return CLASS_E_CLASSNOTAVAILABLE;
	static CKarakuriClassFactory factory;
	return factory.QueryInterface(riid, out);
}

STDAPI DllCanUnloadNow(void) {
	return g_objects == 0 ? S_OK : S_FALSE;
}

// ---------------------------------------------------------------- broker

static void serveOne(HANDLE pipe);

// The broker serves the pipe this copy of the DLL hosts. The agent connects,
// sends one arm frame, and waits for the result frame this side answers once
// winlogon has reported. The secrets never leave the frame; they are zeroed
// as soon as the credential has been consumed.
static DWORD WINAPI BrokerThread(LPVOID)
{
	wchar_t name[64];
	DWORD sessionId = 0;
	ProcessIdToSessionId(GetCurrentProcessId(), &sessionId);
	StringCchPrintfW(name, 64, L"\\\\.\\pipe\\karakuri-unlock-s%lu", (ULONG)sessionId);

	OVERLAPPED ov = {};
	ov.hEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
	for (;;) {
		// One pipe per cycle, created and closed here: an idle gate cycle
		// must not hold the instance, or the next CreateNamedPipe fails on
		// FIRST_PIPE_INSTANCE and the broker stops listening.
		HANDLE pipe = CreateNamedPipeW(name,
			PIPE_ACCESS_DUPLEX | FILE_FLAG_OVERLAPPED | FILE_FLAG_FIRST_PIPE_INSTANCE,
			PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT
				| PIPE_REJECT_REMOTE_CLIENTS,
			1, 4096, 4096, 0, securityAttributes());
		if (pipe == INVALID_HANDLE_VALUE) {
			SetEvent(g_firstPull);
			Sleep(5000);
			continue;
		}
		ResetEvent(ov.hEvent);
		BOOL connected = ConnectNamedPipe(pipe, &ov);
		if (!connected) {
			DWORD err = GetLastError();
			if (err == ERROR_IO_PENDING) {
				// The logon screen's first tile enumeration waits for this
				// exchange, so a queued credential is behind it rather than
				// racing it; an agent that never shows opens the gate empty.
				if (WaitForSingleObject(ov.hEvent, 1500) == WAIT_TIMEOUT) {
					SetEvent(g_firstPull);
					CancelIo(pipe);
					CloseHandle(pipe);
					continue;
				}
			} else if (err != ERROR_PIPE_CONNECTED) {
				SetEvent(g_firstPull);
				CloseHandle(pipe);
				Sleep(1000);
				continue;
			}
		}
		SetEvent(g_firstPull);
		serveOne(pipe);
		FlushFileBuffers(pipe);
		DisconnectNamedPipe(pipe);
		CloseHandle(pipe);
	}
	return 0;
}

// serveOne reads one arm frame, arms the credential, and answers with the
// result winlogon reports — or with the arm timeout. The header is four
// u32s: magic, version, timeout in seconds, payload length.
static void serveOne(HANDLE pipe)
{
	BYTE header[16];
	BYTE rest[2048];
	if (!readFully(pipe, header, sizeof(header))) return;
	if (readU32(header) != frameMagic || readU32(header + 4) != frameVersion) return;
	ULONG timeoutSeconds = readU32(header + 8);
	ULONG payload = readU32(header + 12);
	if (payload > sizeof(rest)) return;
	if (payload && !readFully(pipe, rest, payload)) return;

	ULONG pos = 0;
	wchar_t *user = nullptr, *password = nullptr;
	if (!readPayload(rest, payload, &pos, &user)) return;
	if (!readPayload(rest, payload, &pos, &password)) {
		CoTaskMemFree(user);
		return;
	}

	// Arm under the lock: the bound user is matched against the session's
	// array now, so the enumeration right after the CredentialsChanged
	// signal can create the tile without another round trip.
	EnterCriticalSection(&g_credLock);
	clearQueued();
	g_queued.username = user;
	g_queued.password = password;
	g_queued.user = matchUser(user);
	g_queued.armed = true;
	g_queued.resultSet = false;
	g_queued.consumed = false;
	LeaveCriticalSection(&g_credLock);

	// Tell LogonUI the tiles changed, or it keeps the list it built when the
	// screen came up and never sees the armed credential.
	EnterCriticalSection(&g_credLock);
	ICredentialProviderEvents *events = g_events;
	UINT_PTR context = g_adviseContext;
	if (events) events->AddRef();
	LeaveCriticalSection(&g_credLock);
	if (events) {
		events->CredentialsChanged(context);
		events->Release();
	}
	ResetEvent(g_resultEvent);

	// The answer: winlogon's ReportResult once it has tried the credential,
	// or the arm timeout, whichever comes first.
	WaitForSingleObject(g_resultEvent, timeoutSeconds ? timeoutSeconds * 1000 : 60000);
	EnterCriticalSection(&g_credLock);
	g_queued.armed = false;
	NTSTATUS status = (NTSTATUS)g_queued.resultStatus;
	bool haveResult = g_queued.resultSet;
	bool consumed = g_queued.consumed;
	LeaveCriticalSection(&g_credLock);

	BYTE out[12];
	writeU32(out, frameMagic);
	if (haveResult && NT_SUCCESS(status)) {
		writeU32(out + 4, 0);
	} else if (!haveResult && !consumed) {
		writeU32(out + 4, 2);  // timed out without being submitted
	} else {
		writeU32(out + 4, 1);  // winlogon refused the credential
	}
	writeU32(out + 8, (ULONG)status);
	writeFully(pipe, out, sizeof(out));
}

BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID)
{
	switch (reason) {
	case DLL_PROCESS_ATTACH:
		g_hModule = instance;
		DisableThreadLibraryCalls(instance);
		InitializeCriticalSection(&g_credLock);
		g_resultEvent = CreateEventW(nullptr, TRUE, FALSE, nullptr);
		g_firstPull = CreateEventW(nullptr, TRUE, TRUE, nullptr);
		g_authPackage = resolveNegotiatePackage();
		{
			HANDLE thread = CreateThread(nullptr, 0, BrokerThread, nullptr, 0, nullptr);
			if (thread) CloseHandle(thread);
		}
		return TRUE;
	case DLL_PROCESS_DETACH:
		DeleteCriticalSection(&g_credLock);
		return TRUE;
	}
	return TRUE;
}
