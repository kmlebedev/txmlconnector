// For every call to a function, recover constant R8D (request type) and the
// string loaded into R9 (request name) from the preceding instructions.
// args: OUTFILE hexaddr
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.*;
import ghidra.program.model.address.*;
import ghidra.program.model.symbol.*;
import ghidra.program.model.scalar.Scalar;
import ghidra.program.model.data.StringDataInstance;
import java.io.*;
import java.util.*;

public class CallArgs extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        Address target = toAddr(Long.parseUnsignedLong(a[1], 16));
        Listing listing = currentProgram.getListing();
        TreeSet<String> rows = new TreeSet<>();
        for (Reference r : currentProgram.getReferenceManager().getReferencesTo(target)) {
            if (!r.getReferenceType().isCall()) continue;
            Instruction ins = listing.getInstructionAt(r.getFromAddress());
            String type = "?", name = "?";
            for (int i = 0; i < 25 && ins != null; i++) {
                ins = ins.getPrevious();
                if (ins == null) break;
                String m = ins.getMnemonicString();
                if (ins.getNumOperands() < 2) continue;
                String dst = ins.getDefaultOperandRepresentation(0);
                if (type.equals("?") && (dst.equals("R8D") || dst.equals("R8")) && m.equals("MOV")) {
                    Scalar s = ins.getScalar(1);
                    if (s != null) type = Long.toString(s.getUnsignedValue());
                }
                if (name.equals("?") && dst.equals("R9") && m.equals("LEA")) {
                    for (Reference rr : ins.getReferencesFrom()) {
                        Data d = listing.getDataAt(rr.getToAddress());
                        if (d != null && d.hasStringValue())
                            name = StringDataInstance.getStringDataInstance(d).getStringValue();
                    }
                }
                if (!type.equals("?") && !name.equals("?")) break;
            }
            Function f = currentProgram.getFunctionManager().getFunctionContaining(r.getFromAddress());
            rows.add(String.format("%-4s %-28s %s %s", type, name, r.getFromAddress(), f == null ? "?" : f.getName()));
        }
        for (String s : rows) out.println(s);
        out.close();
    }
}
