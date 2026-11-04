// Decompile functions referencing given strings and/or at given addresses.
// args: OUTFILE DEPTH item...   item = "s:substring" or "a:hexaddr"
// DEPTH = how many levels of callees to include (0 = only the function).
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.*;
import ghidra.program.model.listing.*;
import ghidra.program.model.address.*;
import ghidra.program.model.symbol.*;
import ghidra.program.model.data.StringDataInstance;
import java.io.*;
import java.util.*;

public class XrefDecomp extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        int depth = Integer.parseInt(a[1]);
        Listing listing = currentProgram.getListing();
        FunctionManager fm = currentProgram.getFunctionManager();
        ReferenceManager rm = currentProgram.getReferenceManager();
        LinkedHashMap<Function, String> roots = new LinkedHashMap<>();
        for (int i = 2; i < a.length; i++) {
            String it = a[i];
            if (it.startsWith("a:")) {
                Address ad = toAddr(Long.parseUnsignedLong(it.substring(2), 16));
                Function f = fm.getFunctionContaining(ad);
                if (f != null) roots.putIfAbsent(f, it);
                else out.println("// no function at " + it);
                continue;
            }
            String needle = it.substring(2);
            int hits = 0;
            for (Data d : listing.getDefinedData(true)) {
                if (!d.hasStringValue()) continue;
                String v = StringDataInstance.getStringDataInstance(d).getStringValue();
                if (v == null || !v.contains(needle)) continue;
                for (Reference r : rm.getReferencesTo(d.getAddress())) {
                    Function f = fm.getFunctionContaining(r.getFromAddress());
                    out.println("// string " + d.getAddress() + " \"" + v.replace("\n", "\\n") + "\" ref from " + r.getFromAddress() + " in " + (f == null ? "?" : f.getName() + "@" + f.getEntryPoint()));
                    if (f != null) { roots.putIfAbsent(f, it); hits++; }
                }
            }
            if (hits == 0) out.println("// no refs for " + it);
        }
        DecompInterface di = new DecompInterface();
        di.openProgram(currentProgram);
        Set<Function> done = new HashSet<>();
        ArrayDeque<Object[]> q = new ArrayDeque<>();
        for (Function f : roots.keySet()) q.add(new Object[]{f, 0});
        while (!q.isEmpty() && !monitor.isCancelled()) {
            Object[] e = q.poll();
            Function f = (Function) e[0];
            int d = (Integer) e[1];
            if (!done.add(f) || f.isThunk() || f.isExternal()) continue;
            DecompileResults res = di.decompileFunction(f, 120, monitor);
            out.println("\n/* ===== " + f.getName() + " @ " + f.getEntryPoint() + " depth " + d + " ===== */");
            if (res.decompileCompleted()) out.println(res.getDecompiledFunction().getC());
            else out.println("// decompile failed: " + res.getErrorMessage());
            if (d < depth) for (Function c : f.getCalledFunctions(monitor)) q.add(new Object[]{c, d + 1});
        }
        out.println("/* done " + done.size() + " functions */");
        out.close();
    }
}
