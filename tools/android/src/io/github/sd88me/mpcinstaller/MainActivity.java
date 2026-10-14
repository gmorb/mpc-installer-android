package io.github.sd88me.mpcinstaller;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
import android.content.ClipData;
import android.content.DialogInterface;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.res.Configuration;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStreamReader;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;

/**
 * The MPC plugin installer on Android. The installer itself is the same Go program as on a computer (tools/desktop), built
 * for Android and shipped as lib/ABI/libmpcinstaller.so so that Android lets it run. This activity starts it, waits for the
 * link it prints (http://127.0.0.1:PORT/?t=TOKEN), and shows that page full screen. Nothing else is added: the page, the
 * SSH connection to the MPC and the installs are exactly the desktop app's.
 */
public class MainActivity extends Activity {
    private static final String LOCAL = "http://127.0.0.1:";
    private static final int PICK_FILES = 1;
    // the page's --bg and --fg, light and dark, so the bars around it match
    private static final int BG_LIGHT = 0xffedeff2, FG_LIGHT = 0xff16181d, BG_DARK = 0xff15171b, FG_DARK = 0xffeceef1;

    private FrameLayout root;
    private WebView web;
    private ScrollView statusScroll;
    private TextView status;
    private volatile Process server;
    private volatile boolean ready;
    private ValueCallback<Uri[]> pendingFiles;
    private final StringBuilder log = new StringBuilder();

    @Override
    protected void onCreate(Bundle saved) {
        super.onCreate(saved);
        // An install can take minutes over Wi-Fi; a sleeping phone would drop the connection to the MPC half way.
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        root = new FrameLayout(this);
        // Keep the page clear of the status bar, navigation bar and keyboard, also when Android draws the app edge to edge.
        root.setOnApplyWindowInsetsListener(new View.OnApplyWindowInsetsListener() {
            @Override
            public WindowInsets onApplyWindowInsets(View v, WindowInsets in) {
                int[] c = cutout(in);
                v.setPadding(Math.max(in.getSystemWindowInsetLeft(), c[0]), Math.max(in.getSystemWindowInsetTop(), c[1]),
                        Math.max(in.getSystemWindowInsetRight(), c[2]), Math.max(in.getSystemWindowInsetBottom(), c[3]));
                return in.consumeSystemWindowInsets();
            }
        });

        web = new WebView(this);
        web.setVisibility(View.GONE);
        WebSettings ws = web.getSettings();
        ws.setJavaScriptEnabled(true);
        ws.setDomStorageEnabled(true);
        ws.setAllowFileAccess(false);
        ws.setSupportMultipleWindows(false); // links with target=_blank come to shouldOverrideUrlLoading
        web.setWebViewClient(new WebViewClient() {
            @Override
            public void onPageFinished(WebView view, String url) {
                tellPageTheme();
            }

            @Override
            public boolean shouldOverrideUrlLoading(WebView view, String url) {
                if (url.startsWith(LOCAL)) return false;
                openOutside(url); // guides on GitHub and the like: the phone's browser, not this window
                return true;
            }
        });
        // The page's preferences (appearance, banners) live here: its address, and with it the WebView's storage, changes on
        // every start. Only the page this app loads (127.0.0.1) runs in this WebView; other links open in the phone's browser.
        web.addJavascriptInterface(new PageBridge(), "MPCApp");
        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> cb, FileChooserParams p) {
                return chooseFiles(cb, p.getMode() == FileChooserParams.MODE_OPEN_MULTIPLE);
            }
        });
        root.addView(web, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));

        status = new TextView(this);
        status.setTextSize(TypedValue.COMPLEX_UNIT_SP, 16);
        status.setGravity(Gravity.CENTER);
        int pad = (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, 24, getResources().getDisplayMetrics());
        status.setPadding(pad, pad, pad, pad);
        statusScroll = new ScrollView(this);
        statusScroll.setFillViewport(true);
        statusScroll.addView(status, new ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        root.addView(statusScroll, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        setContentView(root);

        applyTheme();
        registerBackGesture();
        startService();
        startServer();
    }

    private boolean systemNight() {
        return (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES;
    }

    /** Dark or light as the person chose it on the page (Light, Dark), or the phone's setting (Match the system). */
    private boolean night() {
        String t = prefs().getString("theme", "auto");
        return "dark".equals(t) || (!"light".equals(t) && systemNight());
    }

    private SharedPreferences prefs() {
        return getSharedPreferences("page", MODE_PRIVATE);
    }

    /** window.MPCApp on the page: get and set a few named preferences; setting the appearance recolours the bars. */
    private class PageBridge {
        private boolean known(String k) {
            return "theme".equals(k) || "banners".equals(k);
        }

        @JavascriptInterface
        public String get(String k) {
            return known(k) ? prefs().getString(k, null) : null;
        }

        @JavascriptInterface
        public void set(String k, String v) {
            if (!known(k) || v == null || v.length() > 16) return;
            prefs().edit().putString(k, v).apply();
            if ("theme".equals(k)) runOnUiThread(new Runnable() {
                @Override
                public void run() {
                    applyTheme();
                }
            });
        }
    }

    /** Bars and background in the phone's light or dark setting (the page follows it through tellPageTheme). */
    private void applyTheme() {
        boolean dark = night();
        int bg = dark ? BG_DARK : BG_LIGHT;
        getWindow().setStatusBarColor(bg);
        getWindow().setNavigationBarColor(bg);
        int flags = 0;
        if (!dark) flags |= View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR | (Build.VERSION.SDK_INT >= 26 ? 0x10 /* LIGHT_NAVIGATION_BAR */ : 0);
        getWindow().getDecorView().setSystemUiVisibility(flags);
        root.setBackgroundColor(bg);
        web.setBackgroundColor(bg);
        status.setTextColor(dark ? FG_DARK : FG_LIGHT);
    }

    /** The page's dark mode follows data-sys, which is more dependable in a WebView than prefers-color-scheme. */
    private void tellPageTheme() {
        if (web.getVisibility() == View.VISIBLE) {
            web.evaluateJavascript("document.documentElement.dataset.sys='" + (systemNight() ? "dark" : "light") + "'", null);
        }
    }

    @Override
    public void onConfigurationChanged(Configuration c) {
        super.onConfigurationChanged(c); // the activity is not recreated (configChanges), so the server and page live on
        applyTheme();
        tellPageTheme();
    }

    /** InstallService keeps the network while the app is open, also behind other apps. */
    private void startService() {
        Intent i = new Intent(this, InstallService.class);
        try {
            if (Build.VERSION.SDK_INT >= 26) startForegroundService(i);
            else startService(i);
        } catch (RuntimeException e) {
            // not allowed right now (rare): the app still works while it is in front
        }
    }

    /** The camera cutout's safe insets (Android 9+), so the page is not drawn under the punch hole in landscape. */
    private static int[] cutout(WindowInsets in) {
        int[] r = new int[4];
        if (Build.VERSION.SDK_INT < 28) return r;
        try {
            Object c = WindowInsets.class.getMethod("getDisplayCutout").invoke(in);
            if (c == null) return r;
            String[] names = {"getSafeInsetLeft", "getSafeInsetTop", "getSafeInsetRight", "getSafeInsetBottom"};
            for (int i = 0; i < 4; i++) r[i] = (Integer) c.getClass().getMethod(names[i]).invoke(c);
        } catch (Exception ignored) {
            // leave it at the system-bar insets
        }
        return r;
    }

    /**
     * Back on Android 13+: apps targeting Android 16 no longer get onBackPressed, only an OnBackInvokedCallback. It is
     * registered through reflection because the app is compiled against an older android.jar (build_apk.sh).
     */
    private void registerBackGesture() {
        if (Build.VERSION.SDK_INT < 33) return; // onBackPressed below
        try {
            final Class<?> cbType = Class.forName("android.window.OnBackInvokedCallback");
            Class<?> dispType = Class.forName("android.window.OnBackInvokedDispatcher");
            Object cb = Proxy.newProxyInstance(cbType.getClassLoader(), new Class<?>[] {cbType}, new InvocationHandler() {
                @Override
                public Object invoke(Object proxy, Method m, Object[] args) {
                    switch (m.getName()) {
                        case "onBackInvoked":
                            handleBack();
                            return null;
                        case "hashCode":
                            return System.identityHashCode(proxy);
                        case "equals":
                            return proxy == args[0];
                        case "toString":
                            return "mpc-installer back";
                        default:
                            return null;
                    }
                }
            });
            Object dispatcher = Activity.class.getMethod("getOnBackInvokedDispatcher").invoke(this);
            dispType.getMethod("registerOnBackInvokedCallback", int.class, cbType).invoke(dispatcher, 0 /* PRIORITY_DEFAULT */, cb);
        } catch (Exception ignored) {
            // older behaviour: onBackPressed
        }
    }

    private void showStatus(String text, boolean tapToRetry) {
        web.setVisibility(View.GONE);
        statusScroll.setVisibility(View.VISIBLE);
        status.setText(text);
        status.setGravity(tapToRetry ? Gravity.START : Gravity.CENTER);
        status.setOnClickListener(tapToRetry ? new View.OnClickListener() {
            @Override
            public void onClick(View v) {
                startServer();
            }
        } : null);
    }

    /** Starts the installer program and loads its page once it prints the link. */
    private void startServer() {
        ready = false;
        synchronized (log) {
            log.setLength(0);
        }
        showStatus("Starting the installer…", false);
        final File exe = new File(getApplicationInfo().nativeLibraryDir, "libmpcinstaller.so");
        final File cache = getCacheDir();
        final File home = getFilesDir();
        final String dns = dnsServers();
        new Thread("mpc-installer") {
            @Override
            public void run() {
                cleanOldWork(cache);
                ProcessBuilder pb = new ProcessBuilder(exe.getPath(), "-no-browser");
                pb.redirectErrorStream(true);
                pb.directory(home);
                Map<String, String> env = pb.environment();
                env.put("HOME", home.getPath());
                env.put("TMPDIR", cache.getPath()); // the work folder for downloads and uploaded zips
                env.put("MPC_DNS", dns);
                // HTTPS roots for the catalog: Android 14+ keeps the current ones in the Conscrypt APEX. The arm64 build knows
                // the system folder by itself, the 32-bit ARM and x86_64 builds (plain Linux programs) only through this.
                env.put("SSL_CERT_DIR", "/apex/com.android.conscrypt/cacerts:/system/etc/security/cacerts");
                int code;
                try {
                    Process p = pb.start();
                    server = p;
                    BufferedReader r = new BufferedReader(new InputStreamReader(p.getInputStream(), "UTF-8"));
                    String line;
                    while ((line = r.readLine()) != null) {
                        synchronized (log) {
                            if (log.length() < 20000) log.append(line).append('\n');
                        }
                        int i = line.indexOf(LOCAL);
                        if (!ready && i >= 0) {
                            ready = true;
                            final String link = line.substring(i).trim();
                            runOnUiThread(new Runnable() {
                                @Override
                                public void run() {
                                    statusScroll.setVisibility(View.GONE);
                                    web.setVisibility(View.VISIBLE);
                                    web.loadUrl(link);
                                }
                            });
                        }
                    }
                    code = p.waitFor();
                } catch (IOException e) {
                    stopped("could not start: " + e.getMessage());
                    return;
                } catch (InterruptedException e) {
                    return;
                }
                stopped("exit code " + code);
            }
        }.start();
    }

    private void stopped(final String why) {
        server = null;
        if (isFinishing()) return;
        final String out;
        synchronized (log) {
            out = log.toString().trim();
        }
        runOnUiThread(new Runnable() {
            @Override
            public void run() {
                if (isFinishing() || isDestroyed()) return;
                showStatus("The installer stopped (" + why + ").\n\nTap here to start it again.\n\n" + out, true);
            }
        });
    }

    /** Work folders left behind when Android ended the app in the middle of something. */
    private static void cleanOldWork(File cache) {
        File[] old = cache.listFiles();
        if (old == null) return;
        for (File f : old) {
            if (f.getName().startsWith("mpc-installer-")) deleteTree(f);
        }
    }

    private static void deleteTree(File f) {
        File[] kids = f.isDirectory() ? f.listFiles() : null;
        if (kids != null) {
            for (File k : kids) deleteTree(k);
        }
        //noinspection ResultOfMethodCallIgnored
        f.delete();
    }

    /** The current network's DNS servers, comma-separated, for the installer's catalog downloads (dns.go). */
    private String dnsServers() {
        List<String> out = new ArrayList<>();
        try {
            ConnectivityManager cm = (ConnectivityManager) getSystemService(CONNECTIVITY_SERVICE);
            Network n = cm.getActiveNetwork();
            LinkProperties lp = n == null ? null : cm.getLinkProperties(n);
            if (lp != null) {
                for (InetAddress a : lp.getDnsServers()) out.add(a.getHostAddress());
            }
        } catch (RuntimeException ignored) {
            // no network yet: fall back to public servers below
        }
        if (out.isEmpty()) {
            out.add("1.1.1.1");
            out.add("8.8.8.8");
        }
        StringBuilder b = new StringBuilder();
        for (String s : out) {
            if (b.length() > 0) b.append(',');
            b.append(s);
        }
        return b.toString();
    }

    private void openOutside(String url) {
        try {
            startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url)));
        } catch (ActivityNotFoundException ignored) {
            // nothing on the phone opens it
        }
    }

    /** The page's "choose plugin zips" button: the phone's file picker. */
    private boolean chooseFiles(ValueCallback<Uri[]> cb, boolean multiple) {
        if (pendingFiles != null) pendingFiles.onReceiveValue(null);
        pendingFiles = cb;
        Intent i = new Intent(Intent.ACTION_GET_CONTENT);
        i.addCategory(Intent.CATEGORY_OPENABLE);
        i.setType("*/*");
        i.putExtra(Intent.EXTRA_MIME_TYPES, new String[] {
                "application/zip", "application/x-zip-compressed", "application/x-zip", "application/octet-stream"});
        i.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, multiple);
        try {
            startActivityForResult(i, PICK_FILES);
        } catch (ActivityNotFoundException e) {
            pendingFiles = null;
            cb.onReceiveValue(null);
        }
        return true;
    }

    @Override
    protected void onActivityResult(int request, int result, Intent data) {
        if (request != PICK_FILES || pendingFiles == null) {
            super.onActivityResult(request, result, data);
            return;
        }
        Uri[] picked = null;
        if (result == RESULT_OK && data != null) {
            ClipData clip = data.getClipData();
            if (clip != null && clip.getItemCount() > 0) {
                picked = new Uri[clip.getItemCount()];
                for (int i = 0; i < picked.length; i++) picked[i] = clip.getItemAt(i).getUri();
            } else if (data.getData() != null) {
                picked = new Uri[] {data.getData()};
            }
        }
        pendingFiles.onReceiveValue(picked);
        pendingFiles = null;
    }

    @Override
    public void onBackPressed() {
        handleBack();
    }

    private void handleBack() {
        if (web.getVisibility() == View.VISIBLE && web.canGoBack()) {
            web.goBack();
            return;
        }
        new AlertDialog.Builder(this)
                .setMessage("Quit the MPC installer? An install that is still running stops.")
                .setPositiveButton("Quit", new DialogInterface.OnClickListener() {
                    @Override
                    public void onClick(DialogInterface d, int w) {
                        finish();
                    }
                })
                .setNegativeButton("Stay", null)
                .show();
    }

    @Override
    protected void onDestroy() {
        stopService(new Intent(this, InstallService.class));
        ready = true; // a late link must not load into a destroyed view
        Process p = server;
        if (p != null) p.destroy();
        web.destroy();
        super.onDestroy();
    }
}
