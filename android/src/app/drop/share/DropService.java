package app.drop.share;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import android.provider.Settings;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStreamReader;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.security.SecureRandom;
import java.util.Enumeration;
import java.util.Map;

/** Keeps the Drop server running in the background, like the desktop app does. */
public class DropService extends Service {
    static final String TAG = "Drop";
    static final String CHANNEL = "drop_running";
    static final String ACTION_STOP = "app.drop.share.STOP";

    /** Address of the local interface, set once the server is up. */
    static volatile String url = null;
    static volatile boolean running = false;

    private volatile boolean stopping = false;
    private Process proc;
    private PowerManager.WakeLock wake;
    private WifiManager.MulticastLock multicast;
    private File ifaceFile;
    private String lastIfaces = "";

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            shutdown();
            return START_NOT_STICKY;
        }
        startForeground(1, notification());
        if (!running) {
            running = true;
            stopping = false;
            begin();
        }
        return START_STICKY;
    }

    private Notification notification() {
        NotificationManager nm = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
        Notification.Builder b;
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(new NotificationChannel(CHANNEL, "Drop is running", NotificationManager.IMPORTANCE_LOW));
            b = new Notification.Builder(this, CHANNEL);
        } else {
            b = new Notification.Builder(this);
        }
        PendingIntent open = PendingIntent.getActivity(this, 0, new Intent(this, MainActivity.class),
                PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
        PendingIntent stop = PendingIntent.getService(this, 1,
                new Intent(this, DropService.class).setAction(ACTION_STOP), PendingIntent.FLAG_IMMUTABLE);
        b.setContentTitle("Drop is running")
                .setContentText("Ready to send and receive files")
                .setSmallIcon(android.R.drawable.stat_sys_upload_done)
                .setOngoing(true)
                .setContentIntent(open)
                .addAction(android.R.drawable.ic_menu_close_clear_cancel, "Stop", stop);
        return b.build();
    }

    private void begin() {
        SharedPreferences prefs = getSharedPreferences("drop", MODE_PRIVATE);
        String token = prefs.getString("token", null);
        if (token == null) {
            byte[] raw = new byte[12];
            new SecureRandom().nextBytes(raw);
            StringBuilder sb = new StringBuilder();
            for (byte x : raw) sb.append(String.format("%02x", x));
            token = sb.toString();
            prefs.edit().putString("token", token).apply();
        }
        ifaceFile = new File(getFilesDir(), "ifaces.txt");
        writeIfaces();

        try {
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            if (wm != null) {
                multicast = wm.createMulticastLock("drop");
                multicast.setReferenceCounted(false);
                multicast.acquire();
            }
            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
            wake = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "drop:running");
            wake.setReferenceCounted(false);
            wake.acquire();
        } catch (Exception e) {
            Log.w(TAG, "locks: " + e);
        }

        final String tok = token;
        new Thread(new Runnable() {
            @Override
            public void run() {
                runServer(tok);
            }
        }, "drop-server").start();
        new Thread(new Runnable() {
            @Override
            public void run() {
                while (!stopping) {
                    writeIfaces();
                    try {
                        Thread.sleep(3000);
                    } catch (InterruptedException e) {
                        return;
                    }
                }
            }
        }, "drop-net").start();
    }

    private String deviceName() {
        String n = null;
        try {
            n = Settings.Global.getString(getContentResolver(), "device_name");
        } catch (Exception ignored) {
        }
        if (n == null || n.trim().isEmpty()) n = Build.MODEL;
        return n;
    }

    private void runServer(String token) {
        File lib = new File(getApplicationInfo().nativeLibraryDir, "libdrop.so");
        File base = getExternalFilesDir(null);
        if (base == null) base = getFilesDir();
        File dir = new File(base, "Drop");
        dir.mkdirs();
        File cfg = new File(getFilesDir(), "config.json");
        while (!stopping) {
            try {
                ProcessBuilder pb = new ProcessBuilder(lib.getAbsolutePath(), "--headless", "--no-browser",
                        "--port", "47865", "--name", deviceName(), "--dir", dir.getAbsolutePath(),
                        "--config", cfg.getAbsolutePath());
                Map<String, String> env = pb.environment();
                env.put("DROP_TOKEN", token);
                env.put("DROP_IFACES_FILE", ifaceFile.getAbsolutePath());
                env.put("TMPDIR", getCacheDir().getAbsolutePath());
                env.put("HOME", getFilesDir().getAbsolutePath());
                pb.redirectErrorStream(true);
                proc = pb.start();
                BufferedReader r = new BufferedReader(new InputStreamReader(proc.getInputStream()));
                String line;
                while ((line = r.readLine()) != null) {
                    Log.i(TAG, line);
                    int k = line.indexOf("http://127.0.0.1:");
                    if (k >= 0 && line.contains("open:")) url = line.substring(k).trim();
                }
                proc.waitFor();
            } catch (Exception e) {
                Log.e(TAG, "server: " + e);
            }
            url = null;
            if (stopping) break;
            try {
                Thread.sleep(2000);
            } catch (InterruptedException e) {
                break;
            }
        }
    }

    /** Lists the connections you can share over, for the in-app network chooser. */
    private void writeIfaces() {
        try {
            StringBuilder sb = new StringBuilder();
            Enumeration<NetworkInterface> en = NetworkInterface.getNetworkInterfaces();
            while (en != null && en.hasMoreElements()) {
                NetworkInterface ni = en.nextElement();
                if (!ni.isUp() || ni.isLoopback()) continue;
                String name = friendly(ni.getName());
                if (name == null) continue;
                for (InterfaceAddress ia : ni.getInterfaceAddresses()) {
                    InetAddress a = ia.getAddress();
                    if (a instanceof Inet4Address && !a.isLinkLocalAddress()) {
                        sb.append(name).append(' ').append(a.getHostAddress()).append(' ')
                                .append(ia.getNetworkPrefixLength()).append('\n');
                    }
                }
            }
            String now = sb.toString();
            if (now.equals(lastIfaces) && ifaceFile.exists()) return;
            File tmp = new File(ifaceFile.getPath() + ".tmp");
            FileOutputStream out = new FileOutputStream(tmp);
            out.write(now.getBytes("UTF-8"));
            out.close();
            tmp.renameTo(ifaceFile);
            lastIfaces = now;
        } catch (Exception e) {
            Log.w(TAG, "ifaces: " + e);
        }
    }

    private static String friendly(String n) {
        if (n.startsWith("rmnet") || n.startsWith("ccmni") || n.startsWith("v4-") || n.startsWith("dummy")
                || n.startsWith("tun") || n.startsWith("p2p") || n.startsWith("ip6") || n.startsWith("clat")) return null;
        if (n.equals("wlan0")) return "Wi-Fi";
        if (n.startsWith("ap") || n.startsWith("swlan") || n.equals("wlan1") || n.startsWith("softap")) return "Hotspot";
        if (n.startsWith("eth")) return "Ethernet";
        return n.replace(' ', '_');
    }

    private void shutdown() {
        stopping = true;
        running = false;
        url = null;
        try {
            if (proc != null) proc.destroy();
            if (multicast != null && multicast.isHeld()) multicast.release();
            if (wake != null && wake.isHeld()) wake.release();
        } catch (Exception ignored) {
        }
        stopForeground(true);
        stopSelf();
    }

    @Override
    public void onDestroy() {
        if (running) shutdown();
        super.onDestroy();
    }
}
