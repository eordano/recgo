package dev.eordano.recgo.android;

import android.accessibilityservice.AccessibilityService;
import android.graphics.Rect;
import android.net.LocalServerSocket;
import android.net.LocalSocket;
import android.os.SystemClock;
import android.view.accessibility.AccessibilityEvent;
import android.view.accessibility.AccessibilityNodeInfo;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicLong;
import org.json.*;

/** ADB transport only. Deliberately no Internet permission or input injection. */
public final class RecorderService extends AccessibilityService {
    private final ArrayBlockingQueue<JSONObject> events = new ArrayBlockingQueue<>(256);
    private final AtomicLong dropped = new AtomicLong();
    private volatile Set<String> packages = Collections.emptySet();
    private volatile boolean running;
    private volatile LocalServerSocket server;
    private volatile LocalSocket client;

    @Override protected void onServiceConnected() {
        if (running) return;
        running = true;
        configure(Collections.emptySet());
        Thread thread = new Thread(this::serve, "recgo-adb");
        thread.setDaemon(true);
        thread.start();
    }

    private void configure(Set<String> allowed) {
        // Keep the framework subscription stable. Toggling eventTypes from 0
        // can leave an already-visible app's relevant-event cache at 0 until
        // its next window transition. Filter BEFORE inspecting any node below.
        // Idle means no node inspection, queuing or persistence, not unbinding.
        packages = allowed;
    }

    private static JSONObject clock(String kind) throws JSONException {
        JSONObject obj = new JSONObject();
        obj.put("kind", kind);
        obj.put("elapsedMs", SystemClock.elapsedRealtime());
        return obj;
    }

    private void serve() {
        try (LocalServerSocket listener = new LocalServerSocket("recgo_android")) {
            server = listener;
            while (running) {
                try (LocalSocket socket = listener.accept()) {
                    client = socket;
                    int uid = socket.getPeerCredentials().getUid();
                    if (uid != 0 && uid != 2000) {
                        android.util.Log.w("RecgoAndroid", "Rejected non-ADB local peer uid=" + uid);
                        continue;
                    } // adbd root or shell, never other apps
                    socket.setSoTimeout(5000);
                    BufferedReader reader = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8));
                    StringBuilder line = new StringBuilder();
                    for (int c; (c = reader.read()) != -1 && c != '\n';) {
                        if (line.length() >= 4096) throw new IOException("handshake too large");
                        line.append((char)c);
                    }
                    JSONObject request = new JSONObject(line.toString());
                    if (request.getInt("version") != 1) throw new IOException("protocol version");
                    JSONArray names = request.getJSONArray("packages");
                    if (names.length() == 0 || names.length() > 16) throw new IOException("package count");
                    Set<String> allow = new HashSet<>();
                    for (int i = 0; i < names.length(); i++) {
                        String name = names.getString(i);
                        if (!name.matches("[A-Za-z][A-Za-z0-9_]*(\\.[A-Za-z][A-Za-z0-9_]*)+"))
                            throw new IOException("invalid package");
                        allow.add(name);
                    }
                    events.clear();
                    dropped.set(0);
                    configure(Collections.unmodifiableSet(allow));
                    BufferedWriter writer = new BufferedWriter(new OutputStreamWriter(socket.getOutputStream(), StandardCharsets.UTF_8));
                    JSONObject hello = clock("hello");
                    hello.put("version", 1);
                    send(writer, hello);
                    // Client sends one keepalive line per second. Read deadlines also bound idle sessions.
                    while (running) {
                        String ping = reader.readLine();
                        if (!"ping".equals(ping)) break;
                        List<JSONObject> batch = new ArrayList<>();
                        events.drainTo(batch);
                        for (JSONObject event : batch) send(writer, event);
                        JSONObject heartbeat = clock("heartbeat");
                        heartbeat.put("dropped", dropped.getAndSet(0));
                        send(writer, heartbeat);
                    }
                } catch (Exception ignored) {
                    // Transport diagnostics only; the event payload never enters this exception path.
                    android.util.Log.w("RecgoAndroid", "Recorder connection ended", ignored);
                } finally {
                    packages = Collections.emptySet();
                    events.clear();
                    client = null;
                }
            }
        } catch (IOException ignored) {
            android.util.Log.w("RecgoAndroid", "Local recorder listener stopped", ignored);
        } finally { running = false; }
    }

    private static void send(BufferedWriter out, JSONObject obj) throws IOException {
        out.write(obj.toString()); out.write('\n'); out.flush();
    }

    @Override public void onAccessibilityEvent(AccessibilityEvent event) {
        Set<String> active = packages;
        String pkg = String.valueOf(event.getPackageName());
        if (!active.contains(pkg)) return;
        try {
            JSONObject obj = clock(kind(event.getEventType()));
            obj.put("elapsedMs", SystemClock.elapsedRealtime() - (SystemClock.uptimeMillis() - event.getEventTime()));
            obj.put("package", pkg);
            obj.put("windowId", event.getWindowId());
            AccessibilityNodeInfo source = event.getSource();
            boolean sensitive = event.isPassword();
            if (source != null) {
                AccessibilityNodeInfo cursor = AccessibilityNodeInfo.obtain(source);
                try {
                    for (int depth = 0; cursor != null && depth < 16; depth++) {
                        sensitive |= cursor.isPassword() || cursor.isEditable();
                        AccessibilityNodeInfo parent = cursor.getParent();
                        cursor.recycle(); cursor = parent;
                    }
                    // An over-deep path is conservatively redacted.
                    if (cursor != null) sensitive = true;
                } finally { if (cursor != null) cursor.recycle(); }
                try { obj.put("node", describe(source, sensitive)); }
                finally { source.recycle(); }
                obj.put("targetStatus", "accessibility-source");
            } else obj.put("targetStatus", "unknown");
            // Do not fall back to event.getText(): it may contain typed secrets.
            if (packages == active && !events.offer(obj)) dropped.incrementAndGet();
        } catch (Exception ignored) { dropped.incrementAndGet(); }
    }

    private static String kind(int type) {
        switch (type) {
            case AccessibilityEvent.TYPE_VIEW_CLICKED: return "click";
            case AccessibilityEvent.TYPE_VIEW_LONG_CLICKED: return "long-click";
            case AccessibilityEvent.TYPE_VIEW_SCROLLED: return "scroll";
            default: return "window";
        }
    }

    private static String safe(CharSequence value) {
        if (value == null) return "";
        String s = value.toString();
        return s.substring(0, Math.min(512, s.length()));
    }

    private static JSONObject describe(AccessibilityNodeInfo node, boolean sensitive) throws JSONException {
        JSONObject obj = new JSONObject();
        Rect bounds = new Rect(); node.getBoundsInScreen(bounds);
        obj.put("resourceId", safe(node.getViewIdResourceName()));
        obj.put("class", safe(node.getClassName()));
        obj.put("bounds", new JSONArray(new int[]{bounds.left, bounds.top, bounds.right, bounds.bottom}));
        obj.put("clickable", node.isClickable());
        obj.put("enabled", node.isEnabled());
        obj.put("checked", node.isChecked());
        obj.put("redacted", sensitive);
        if (!sensitive) {
            obj.put("text", safe(node.getText()));
            obj.put("description", safe(node.getContentDescription()));
        }
        return obj;
    }

    @Override public void onInterrupt() { }
    @Override public void onDestroy() {
        running = false;
        packages = Collections.emptySet();
        final LocalSocket closingClient = client;
        final LocalServerSocket closingServer = server;
        // Never wait for native socket I/O while holding Android's main thread.
        Thread closer = new Thread(() -> {
            try { if (closingClient != null) {
                closingClient.shutdownInput(); closingClient.shutdownOutput(); closingClient.close();
            } } catch (IOException ignored) { }
            try { if (closingServer != null) {
                android.system.Os.shutdown(closingServer.getFileDescriptor(), android.system.OsConstants.SHUT_RDWR);
            } } catch (Exception ignored) { }
            try { if (closingServer != null) closingServer.close(); } catch (IOException ignored) { }
        }, "recgo-close");
        closer.setDaemon(true); closer.start();
        super.onDestroy();
    }
}
