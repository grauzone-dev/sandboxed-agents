using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Threading;

namespace SandboxAgents.Tests {
    public sealed class EnvironmentNotification : IDisposable {
        delegate IntPtr WindowProc(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        struct WindowClass {
            public uint style;
            public WindowProc procedure;
            public int classExtra, windowExtra;
            public IntPtr instance, icon, cursor, background;
            public string menu, name;
        }
        [StructLayout(LayoutKind.Sequential)]
        struct Message {
            public IntPtr window;
            public uint message;
            public UIntPtr wParam;
            public IntPtr lParam;
            public uint time;
            public int x, y;
            public uint privateData;
        }
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode)]
        static extern IntPtr GetModuleHandleW(string module);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern ushort RegisterClassW(ref WindowClass windowClass);
        [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        static extern IntPtr CreateWindowExW(uint extendedStyle, string className, string title,
            uint style, int x, int y, int width, int height, IntPtr parent, IntPtr menu,
            IntPtr instance, IntPtr parameter);
        [DllImport("user32.dll")]
        static extern int GetMessageW(out Message message, IntPtr window, uint minimum, uint maximum);
        [DllImport("user32.dll")]
        static extern IntPtr DispatchMessageW(ref Message message);
        [DllImport("user32.dll")]
        static extern IntPtr DefWindowProcW(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
        [DllImport("user32.dll")]
        static extern void PostQuitMessage(int code);
        [DllImport("user32.dll")]
        static extern bool PostMessageW(IntPtr window, uint message, UIntPtr wParam, IntPtr lParam);
        [DllImport("user32.dll")]
        static extern bool DestroyWindow(IntPtr window);
        [DllImport("user32.dll", CharSet = CharSet.Unicode)]
        static extern bool UnregisterClassW(string name, IntPtr instance);

        readonly ManualResetEventSlim ready = new ManualResetEventSlim(false);
        readonly Thread thread;
        readonly WindowProc procedure;
        IntPtr window;
        Exception failure;
        int count;
        public int Count { get { return Volatile.Read(ref count); } }

        public EnvironmentNotification() {
            procedure = Receive;
            thread = new Thread(Pump) { IsBackground = true };
            thread.Start();
            if (!ready.Wait(TimeSpan.FromSeconds(10))) throw new TimeoutException("Notification receiver did not start.");
            if (failure != null) throw new InvalidOperationException("Notification receiver failed.", failure);
        }

        IntPtr Receive(IntPtr handle, uint message, UIntPtr wParam, IntPtr lParam) {
            if (message == 0x001a && Marshal.PtrToStringUni(lParam) == "Environment") {
                Interlocked.Increment(ref count);
            }
            if (message == 0x0002) PostQuitMessage(0);
            return DefWindowProcW(handle, message, wParam, lParam);
        }

        void Pump() {
            string name = "SandboxAgentsNotificationTest-" + Guid.NewGuid().ToString("N");
            IntPtr instance = GetModuleHandleW(null);
            ushort atom = 0;
            try {
                WindowClass definition = new WindowClass { procedure = procedure, instance = instance, name = name };
                atom = RegisterClassW(ref definition);
                if (atom == 0) throw new Win32Exception(Marshal.GetLastWin32Error());
                window = CreateWindowExW(0, name, "", 0, 0, 0, 0, 0, IntPtr.Zero, IntPtr.Zero, instance, IntPtr.Zero);
                if (window == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
                ready.Set();
                Message message;
                int result;
                while ((result = GetMessageW(out message, IntPtr.Zero, 0, 0)) > 0) DispatchMessageW(ref message);
                if (result < 0) throw new Win32Exception(Marshal.GetLastWin32Error());
            } catch (Exception error) {
                failure = error;
            } finally {
                ready.Set();
                if (window != IntPtr.Zero) DestroyWindow(window);
                if (atom != 0) UnregisterClassW(name, instance);
            }
        }

        public void Dispose() {
            if (window != IntPtr.Zero) PostMessageW(window, 0x0010, UIntPtr.Zero, IntPtr.Zero);
            if (!thread.Join(TimeSpan.FromSeconds(5))) throw new TimeoutException("Notification receiver did not stop.");
            ready.Dispose();
            if (failure != null) throw new InvalidOperationException("Notification receiver failed.", failure);
        }
    }
}
