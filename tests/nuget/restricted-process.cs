using System;
using System.Collections;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;

public static class NuGetRestrictedProcess
{
    private const uint TOKEN_ASSIGN_PRIMARY = 0x0001;
    private const uint TOKEN_DUPLICATE = 0x0002;
    private const uint TOKEN_QUERY = 0x0008;
    private const uint DISABLE_MAX_PRIVILEGE = 0x0001;
    private const uint LUA_TOKEN = 0x0004;
    private const uint CREATE_NO_WINDOW = 0x08000000;
    private const uint CREATE_UNICODE_ENVIRONMENT = 0x00000400;

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct StartupInfo
    {
        public uint Size;
        public string Reserved;
        public string Desktop;
        public string Title;
        public uint X, Y, XSize, YSize, XCountChars, YCountChars, FillAttribute, Flags;
        public ushort ShowWindow, ReservedLength;
        public IntPtr ReservedBytes, StandardInput, StandardOutput, StandardError;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct ProcessInformation
    {
        public IntPtr Process, Thread;
        public uint ProcessId, ThreadId;
    }

    [DllImport("kernel32.dll")]
    private static extern IntPtr GetCurrentProcess();

    [DllImport("advapi32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool OpenProcessToken(IntPtr process, uint access, out IntPtr token);

    [DllImport("advapi32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateRestrictedToken(IntPtr existing, uint flags, uint disabledCount, IntPtr disabled,
        uint privilegeCount, IntPtr privileges, uint restrictedCount, IntPtr restricted, out IntPtr token);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true, ExactSpelling = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateProcessAsUserW(IntPtr token, string application, StringBuilder commandLine,
        IntPtr processAttributes, IntPtr threadAttributes, [MarshalAs(UnmanagedType.Bool)] bool inheritHandles,
        uint creationFlags, IntPtr environment, string directory,
        ref StartupInfo startup, out ProcessInformation information);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint WaitForSingleObject(IntPtr handle, uint milliseconds);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetExitCodeProcess(IntPtr process, out uint code);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool TerminateProcess(IntPtr process, uint code);

    [DllImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseHandle(IntPtr handle);

    private static string Quote(string argument)
    {
        var result = new StringBuilder("\"");
        int backslashes = 0;
        foreach (char character in argument)
        {
            if (character == '\\') { backslashes++; continue; }
            result.Append('\\', character == '"' ? backslashes * 2 + 1 : backslashes);
            backslashes = 0;
            result.Append(character);
        }
        result.Append('\\', backslashes * 2);
        return result.Append('"').ToString();
    }

    public static int Run(string executable, string[] arguments, string directory)
    {
        IntPtr original = IntPtr.Zero, restricted = IntPtr.Zero, environment = IntPtr.Zero;
        var process = new ProcessInformation();
        try
        {
            if (!OpenProcessToken(GetCurrentProcess(), TOKEN_ASSIGN_PRIMARY | TOKEN_DUPLICATE | TOKEN_QUERY, out original))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "OpenProcessToken");
            if (!CreateRestrictedToken(original, DISABLE_MAX_PRIVILEGE | LUA_TOKEN, 0, IntPtr.Zero, 0, IntPtr.Zero, 0, IntPtr.Zero, out restricted))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateRestrictedToken");

            var variables = new List<string>();
            foreach (DictionaryEntry entry in Environment.GetEnvironmentVariables())
                variables.Add(entry.Key + "=" + entry.Value);
            variables.Sort(StringComparer.OrdinalIgnoreCase);
            environment = Marshal.StringToHGlobalUni(string.Join("\0", variables) + "\0\0");
            var commandLine = new StringBuilder(Quote(executable));
            foreach (string argument in arguments) commandLine.Append(' ').Append(Quote(argument));
            var startup = new StartupInfo { Size = (uint)Marshal.SizeOf<StartupInfo>() };
            if (!CreateProcessAsUserW(restricted, executable, commandLine, IntPtr.Zero, IntPtr.Zero, false, CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                environment, directory, ref startup, out process))
            {
                int error = Marshal.GetLastWin32Error();
                throw new Win32Exception(error, "CreateProcessAsUserW NativeErrorCode=" +
                    error + " NativeMessage=" + new Win32Exception(error).Message);
            }
            uint waited = WaitForSingleObject(process.Process, 180000);
            if (waited != 0)
            {
                TerminateProcess(process.Process, 1);
                WaitForSingleObject(process.Process, 10000);
                throw new InvalidOperationException("Restricted installer test did not complete: wait status " + waited);
            }
            uint exitCode;
            if (!GetExitCodeProcess(process.Process, out exitCode))
                throw new Win32Exception(Marshal.GetLastWin32Error(), "GetExitCodeProcess");
            return (int)exitCode;
        }
        finally
        {
            if (process.Thread != IntPtr.Zero) CloseHandle(process.Thread);
            if (process.Process != IntPtr.Zero) CloseHandle(process.Process);
            if (environment != IntPtr.Zero) Marshal.FreeHGlobal(environment);
            if (restricted != IntPtr.Zero) CloseHandle(restricted);
            if (original != IntPtr.Zero) CloseHandle(original);
        }
    }
}
