// args: OUTFILE exact-string... — exact string matches and referencing instructions
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.*;
import ghidra.program.model.symbol.*;
import ghidra.program.model.data.StringDataInstance;
import java.io.*;
public class StrRefs extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        Listing l = currentProgram.getListing();
        for (Data d : l.getDefinedData(true)) {
            if (!d.hasStringValue()) continue;
            String v = StringDataInstance.getStringDataInstance(d).getStringValue();
            for (int i = 1; i < a.length; i++) if (a[i].equals(v)) {
                out.println("string " + d.getAddress() + " " + v);
                for (Reference r : currentProgram.getReferenceManager().getReferencesTo(d.getAddress())) {
                    Function f = currentProgram.getFunctionManager().getFunctionContaining(r.getFromAddress());
                    Instruction ins = l.getInstructionAt(r.getFromAddress());
                    out.println("  ref " + r.getFromAddress() + " " + (ins == null ? "" : ins.toString()) + " in " + (f == null ? "?" : f.getName()));
                    for (int k = 0; k < 6 && ins != null; k++) { ins = ins.getNext(); if (ins != null) out.println("      " + ins.getAddress() + " " + ins); }
                }
            }
        }
        out.close();
    }
}
