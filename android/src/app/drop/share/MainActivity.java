package app.drop.share;

import android.Manifest;
import android.app.Activity;
import android.app.DownloadManager;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.DownloadListener;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;

import java.net.URLDecoder;

public class MainActivity extends Activity {
    private static final int REQ_FILES = 11;
    private WebView web;
    private ValueCallback<Uri[]> chooser;
    private String loaded;
    private final Handler handler = new Handler(Looper.getMainLooper());

    private final Runnable poll = new Runnable() {
        @Override
        public void run() {
            String u = DropService.url;
            if (u != null && !u.equals(loaded)) {
                loaded = u;
                web.loadUrl(u);
            }
            handler.postDelayed(this, 500);
        }
    };

    @Override
    protected void onCreate(android.os.Bundle state) {
        super.onCreate(state);
        boolean night = (getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK)
                == Configuration.UI_MODE_NIGHT_YES;
        web = new WebView(this);
        web.setLayoutParams(new ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        web.setBackgroundColor(Color.parseColor("#0E1120"));
        setContentView(web);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setAllowFileAccessFromFileURLs(false);
        s.setAllowUniversalAccessFromFileURLs(false);
        s.setGeolocationEnabled(false);
        s.setMediaPlaybackRequiresUserGesture(false);

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest req) {
                Uri u = req.getUrl();
                if ("127.0.0.1".equals(u.getHost())) return false;
                String sc = u.getScheme();
                if ("http".equals(sc) || "https".equals(sc)) {
                    try {
                        startActivity(new Intent(Intent.ACTION_VIEW, u));
                    } catch (Exception ignored) {
                    }
                }
                return true;
            }

            @Override
            public void onReceivedError(WebView v, WebResourceRequest req, WebResourceError err) {
                if (req.isForMainFrame()) {
                    handler.postDelayed(new Runnable() {
                        @Override
                        public void run() {
                            loaded = null; // try again once the server answers
                        }
                    }, 1500);
                }
            }
        });

        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView v, ValueCallback<Uri[]> cb, FileChooserParams params) {
                if (chooser != null) chooser.onReceiveValue(null);
                chooser = cb;
                try {
                    startActivityForResult(params.createIntent(), REQ_FILES);
                } catch (Exception e) {
                    chooser = null;
                    return false;
                }
                return true;
            }
        });

        web.setDownloadListener(new DownloadListener() {
            @Override
            public void onDownloadStart(String url, String ua, String disp, String mime, long len) {
                download(url, disp, mime);
            }
        });

        askPermissions();
        Intent svc = new Intent(this, DropService.class);
        if (Build.VERSION.SDK_INT >= 26) startForegroundService(svc);
        else startService(svc);
    }

    private void askPermissions() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission("android.permission.POST_NOTIFICATIONS") != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{"android.permission.POST_NOTIFICATIONS"}, 1);
        } else if (Build.VERSION.SDK_INT < 29 && checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 2);
        }
    }

    private void download(String url, String disp, String mime) {
        try {
            String name = null;
            if (disp != null) {
                int k = disp.indexOf("filename*=UTF-8''");
                if (k >= 0) {
                    String v = disp.substring(k + 17);
                    int e = v.indexOf(';');
                    if (e >= 0) v = v.substring(0, e);
                    name = URLDecoder.decode(v.replace("+", "%2B"), "UTF-8");
                }
            }
            if (name == null) name = android.webkit.URLUtil.guessFileName(url, disp, mime);
            DownloadManager.Request r = new DownloadManager.Request(Uri.parse(url));
            String cookie = CookieManager.getInstance().getCookie(url);
            if (cookie != null) r.addRequestHeader("Cookie", cookie);
            r.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            r.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
            ((DownloadManager) getSystemService(DOWNLOAD_SERVICE)).enqueue(r);
            Toast.makeText(this, "Saving " + name + " to Downloads", Toast.LENGTH_SHORT).show();
        } catch (Exception e) {
            Toast.makeText(this, "Couldn't save the file: " + e.getMessage(), Toast.LENGTH_LONG).show();
        }
    }

    @Override
    protected void onActivityResult(int req, int res, Intent data) {
        if (req == REQ_FILES && chooser != null) {
            chooser.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(res, data));
            chooser = null;
        }
        super.onActivityResult(req, res, data);
    }

    @Override
    protected void onResume() {
        super.onResume();
        handler.post(poll);
    }

    @Override
    protected void onPause() {
        handler.removeCallbacks(poll);
        super.onPause();
    }

    @Override
    public void onBackPressed() {
        if (web.canGoBack()) web.goBack();
        else moveTaskToBack(true); // keep sharing in the background
    }
}
