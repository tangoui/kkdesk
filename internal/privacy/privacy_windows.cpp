//go:build windows && cgo

#define WIN32_LEAN_AND_MEAN
#include <windows.h>

#include <algorithm>
#include <cwchar>
#include <memory>
#include <vector>
#include "privacy_windows.h"

static const wchar_t className[] = L"KKDeskPrivacyScreen";
static constexpr UINT stopMessage = WM_APP + 1;
static constexpr UINT rebuildMessage = WM_APP + 2;
static constexpr UINT countdownMessage = WM_APP + 3;
static constexpr UINT_PTR topmostTimer = 1;
static constexpr int exitHotkey = 1;
static constexpr DWORD excludeFromCapture = 0x11;
static const wchar_t countdownProperty[] = L"KKDeskLockCountdown";

struct WindowsScreen {
    HWND control = nullptr;
    std::vector<HWND> covers;
    DWORD error = ERROR_SUCCESS;
    bool shortcut = false;
    int countdown = -1;

    ~WindowsScreen() {
        for (HWND cover : covers) DestroyWindow(cover);
        if (control) {
            UnregisterHotKey(control, exitHotkey);
            DestroyWindow(control);
        }
    }
};

struct CoverList {
    std::vector<HWND> windows;
    DWORD error = ERROR_SUCCESS;
    ~CoverList() {
        for (HWND window : windows) DestroyWindow(window);
    }
};

static BOOL CALLBACK makeCover(HMONITOR, HDC, LPRECT bounds, LPARAM cookie) {
    CoverList &list = *reinterpret_cast<CoverList *>(cookie);
    HWND cover = CreateWindowExW(
        WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE,
        className, L"", WS_POPUP, bounds->left, bounds->top,
        bounds->right - bounds->left, bounds->bottom - bounds->top,
        nullptr, nullptr, GetModuleHandleW(nullptr), nullptr);
    if (!cover) {
        list.error = GetLastError();
        return FALSE;
    }
    list.windows.push_back(cover);
    if (!SetLayeredWindowAttributes(cover, 0, 254, LWA_ALPHA) ||
        !SetWindowDisplayAffinity(cover, excludeFromCapture)) {
        list.error = GetLastError();
        return FALSE;
    }
    DWORD affinity = 0;
    if (!GetWindowDisplayAffinity(cover, &affinity) || affinity != excludeFromCapture) {
        list.error = ERROR_NOT_SUPPORTED;
        return FALSE;
    }
    return TRUE;
}

static DWORD rebuild(WindowsScreen &screen) {
    CoverList list;
    if (!EnumDisplayMonitors(nullptr, nullptr, makeCover, reinterpret_cast<LPARAM>(&list)) ||
        list.windows.empty()) {
        return list.error == ERROR_SUCCESS ? ERROR_NOT_SUPPORTED : list.error;
    }
    for (HWND window : list.windows) {
        if (screen.countdown >= 0) {
            SetPropW(window, countdownProperty, reinterpret_cast<HANDLE>(static_cast<INT_PTR>(screen.countdown + 1)));
        }
        if (!SetWindowPos(window, HWND_TOPMOST, 0, 0, 0, 0,
                          SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_SHOWWINDOW)) {
            DWORD error = GetLastError();
            return error == ERROR_SUCCESS ? ERROR_NOT_SUPPORTED : error;
        }
    }
    screen.covers.swap(list.windows);
    return ERROR_SUCCESS;
}

static void paintCover(HWND hwnd) {
    PAINTSTRUCT ps;
    HDC dc = BeginPaint(hwnd, &ps);
    RECT rect;
    GetClientRect(hwnd, &rect);
    FillRect(dc, &rect, static_cast<HBRUSH>(GetStockObject(BLACK_BRUSH)));
    int height = -MulDiv(18, GetDpiForWindow(hwnd), 72);
    HFONT font = CreateFontW(height, 0, 0, 0, FW_NORMAL, FALSE, FALSE, FALSE,
                           DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
                           CLEARTYPE_QUALITY, DEFAULT_PITCH, L"Microsoft YaHei UI");
    HGDIOBJ old = font ? SelectObject(dc, font) : nullptr;
    SetTextColor(dc, RGB(255, 255, 255));
    SetBkMode(dc, TRANSPARENT);
    int countdown = static_cast<int>(reinterpret_cast<INT_PTR>(GetPropW(hwnd, countdownProperty))) - 1;
    wchar_t graceText[128];
    const wchar_t *text = L"正在被远程控制，屏幕已隐藏。\n按 Ctrl+Alt+Shift+P 结束隐私屏";
    if (countdown == 0) {
        text = L"正在锁屏…";
    } else if (countdown > 0) {
        std::swprintf(graceText, 128, L"连接已断开，%d 秒后锁屏\n按 Ctrl+Alt+Shift+P 取消锁屏并结束隐私屏", countdown);
        text = graceText;
    }
    RECT textBounds = rect;
    DrawTextW(dc, text, -1, &textBounds, DT_CENTER | DT_WORDBREAK | DT_CALCRECT);
    rect.top += std::max<LONG>(0, (rect.bottom - rect.top - (textBounds.bottom - textBounds.top)) / 2);
    DrawTextW(dc, text, -1, &rect, DT_CENTER | DT_WORDBREAK);
    if (old) SelectObject(dc, old);
    if (font) DeleteObject(font);
    EndPaint(hwnd, &ps);
}

static LRESULT CALLBACK windowProc(HWND hwnd, UINT message, WPARAM wparam, LPARAM lparam) {
    if (message == WM_NCCREATE) {
        auto *data = reinterpret_cast<CREATESTRUCTW *>(lparam);
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(data->lpCreateParams));
    }
    auto *screen = reinterpret_cast<WindowsScreen *>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
    if (!screen) {
        switch (message) {
        case WM_MOUSEACTIVATE: return MA_NOACTIVATE;
        case WM_NCHITTEST: return HTTRANSPARENT;
        case WM_PAINT:
            paintCover(hwnd);
            return 0;
        }
        return DefWindowProcW(hwnd, message, wparam, lparam);
    }
    switch (message) {
    case stopMessage:
        PostQuitMessage(0);
        return 0;
    case WM_HOTKEY:
        if (wparam == exitHotkey) {
            screen->shortcut = true;
            PostQuitMessage(0);
        }
        return 0;
    case WM_DISPLAYCHANGE:
        PostMessageW(hwnd, rebuildMessage, 0, 0);
        return 0;
    case rebuildMessage:
        screen->error = rebuild(*screen);
        if (screen->error != ERROR_SUCCESS) PostQuitMessage(0);
        return 0;
    case countdownMessage:
        screen->countdown = static_cast<int>(wparam);
        for (HWND cover : screen->covers) {
            SetPropW(cover, countdownProperty, reinterpret_cast<HANDLE>(static_cast<INT_PTR>(screen->countdown + 1)));
            InvalidateRect(cover, nullptr, FALSE);
        }
        return 0;
    case WM_TIMER: {
        HWND top = GetTopWindow(nullptr);
        if (std::find(screen->covers.begin(), screen->covers.end(), top) == screen->covers.end()) {
            for (HWND cover : screen->covers) {
                SetWindowPos(cover, HWND_TOPMOST, 0, 0, 0, 0,
                             SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_NOOWNERZORDER);
            }
        }
        return 0;
    }
    }
    return DefWindowProcW(hwnd, message, wparam, lparam);
}

void *mmprivacyw_open(uintptr_t *window, unsigned long *error) {
    SetThreadDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
    HINSTANCE module = GetModuleHandleW(nullptr);
    WNDCLASSW wc = {};
    wc.lpfnWndProc = windowProc;
    wc.hInstance = module;
    wc.hbrBackground = static_cast<HBRUSH>(GetStockObject(BLACK_BRUSH));
    wc.lpszClassName = className;
    if (!RegisterClassW(&wc) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) {
        *error = GetLastError();
        return nullptr;
    }
    auto screen = std::make_unique<WindowsScreen>();
    // A hidden top-level window receives display broadcasts and the hotkey.
    screen->control = CreateWindowExW(WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE, className, L"",
                                      WS_POPUP, 0, 0, 0, 0, nullptr, nullptr, module, screen.get());
    if (!screen->control ||
        !RegisterHotKey(screen->control, exitHotkey, MOD_CONTROL | MOD_ALT | MOD_SHIFT | MOD_NOREPEAT, 'P') ||
        !SetTimer(screen->control, topmostTimer, 350, nullptr)) {
        *error = GetLastError();
        return nullptr;
    }
    *error = rebuild(*screen);
    if (*error != ERROR_SUCCESS) return nullptr;
    *window = reinterpret_cast<uintptr_t>(screen->control);
    return screen.release();
}

unsigned long mmprivacyw_run(void *ptr) {
    std::unique_ptr<WindowsScreen> screen(static_cast<WindowsScreen *>(ptr));
    MSG msg;
    BOOL result;
    while ((result = GetMessageW(&msg, nullptr, 0, 0)) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    if (result == -1) return GetLastError();
    if (screen->error != ERROR_SUCCESS) return screen->error;
    // ERROR_CANCELLED is the local shortcut. A normal stop has no callback.
    return screen->shortcut ? ERROR_CANCELLED : ERROR_SUCCESS;
}

void mmprivacyw_stop(uintptr_t window) {
    PostMessageW(reinterpret_cast<HWND>(window), stopMessage, 0, 0);
}

void mmprivacyw_countdown(uintptr_t window, int seconds) {
    PostMessageW(reinterpret_cast<HWND>(window), countdownMessage, static_cast<WPARAM>(seconds), 0);
}

#include <wtsapi32.h>

unsigned long mmprivacyw_lock(void) {
    if (LockWorkStation()) return ERROR_SUCCESS;
    DWORD err = GetLastError();
    return err != ERROR_SUCCESS ? err : ERROR_GEN_FAILURE;
}

unsigned long mmprivacyw_locked(int *locked) {
    LPWSTR buffer = nullptr;
    DWORD size = 0;
    if (!WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, WTS_CURRENT_SESSION,
                                    WTSSessionInfoEx, &buffer, &size)) {
        DWORD err = GetLastError();
        return err != ERROR_SUCCESS ? err : ERROR_GEN_FAILURE;
    }
    auto *info = reinterpret_cast<const WTSINFOEXW *>(buffer);
    DWORD error = ERROR_INVALID_DATA;
    if (size >= sizeof(WTSINFOEXW) && info->Level == 1) {
        LONG flag = info->Data.WTSInfoExLevel1.SessionFlags;
        if (flag == WTS_SESSIONSTATE_LOCK || flag == WTS_SESSIONSTATE_UNLOCK) {
            *locked = flag == WTS_SESSIONSTATE_LOCK;
            error = ERROR_SUCCESS;
        }
    }
    WTSFreeMemory(buffer);
    return error;
}
