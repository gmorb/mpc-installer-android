package io.github.sd88me.mpcinstaller;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

/**
 * Runs while the app is open, so Android treats it as doing foreground work. Without it, Android 15 and later cut the
 * network of an app that is not in front, and an install copying a plugin to the MPC failed with "software caused
 * connection abort" as soon as the person switched apps. Its notification says the installer is running; tapping it
 * goes back to the app.
 */
public class InstallService extends Service {
    private static final String CHANNEL = "installer";

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        Notification.Builder b;
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationManager nm = getSystemService(NotificationManager.class);
            nm.createNotificationChannel(new NotificationChannel(CHANNEL, "Installer running", NotificationManager.IMPORTANCE_LOW));
            b = new Notification.Builder(this, CHANNEL);
        } else {
            b = new Notification.Builder(this);
        }
        Intent open = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        Notification n = b.setSmallIcon(android.R.drawable.stat_sys_upload)
                .setContentTitle("MPC Installer is running")
                .setContentText("It keeps its connection to your MPC while you use other apps.")
                .setContentIntent(PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_IMMUTABLE))
                .setOngoing(true)
                .build();
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(1, n);
        }
        return START_NOT_STICKY;
    }

    /** Android 15+ ends a data-sync service after 6 hours a day: stop quietly, the app keeps working while in front. */
    @Override
    public void onTimeout(int startId, int fgsType) {
        stopSelf();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
