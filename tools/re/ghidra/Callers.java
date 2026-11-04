// args: OUTFILE hexaddr... — list callers (function name@entry, call site)
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import java.io.*;
public class Callers extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        for (int i = 1; i < a.length; i++) {
            for (Reference r : currentProgram.getReferenceManager().getReferencesTo(toAddr(Long.parseUnsignedLong(a[i], 16)))) {
                Function f = currentProgram.getFunctionManager().getFunctionContaining(r.getFromAddress());
                out.println(a[i] + " <- " + r.getFromAddress() + " " + r.getReferenceType() + " in " + (f == null ? "?" : f.getName() + "@" + f.getEntryPoint()));
            }
        }
        out.close();
    }
}
