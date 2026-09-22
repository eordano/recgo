package dev.eordano.recgo.android;

import android.app.Activity;
import android.os.Bundle;
import android.content.Intent;
import android.provider.Settings;
import android.text.InputType;
import android.widget.*;

public final class MainActivity extends Activity {
    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setPadding(32, 80, 32, 32);
        TextView intro = new TextView(this);
        intro.setText("Recgo Android\n\nEnable the Recgo accessibility service below, then start recgo-android on your computer. Only allowlisted apps are inspected while a recorder is connected. No typing or raw touch interception.\n\nScreenshots/video can show the whole display; app logs can contain secrets. Enable those separately on the desktop.\n");
        root.addView(intro);
        Button settings = new Button(this);
        settings.setText("Accessibility settings");
        settings.setOnClickListener(v -> startActivity(new Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)));
        root.addView(settings);
        TextView status = new TextView(this);
        status.setId(R.id.demo_status);
        status.setText("Demo: ready");
        Button demo = new Button(this);
        demo.setId(R.id.demo_button);
        demo.setText("Test named button");
        demo.setOnClickListener(v -> status.setText("Demo: clicked"));
        root.addView(demo);
        Switch toggle = new Switch(this);
        toggle.setId(R.id.demo_toggle);
        toggle.setText("Test toggle");
        root.addView(toggle);
        EditText password = new EditText(this);
        password.setId(R.id.demo_password);
        password.setHint("Synthetic password (never recorded as metadata)");
        password.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        root.addView(password);
        root.addView(status);
        setContentView(root);
    }
}
