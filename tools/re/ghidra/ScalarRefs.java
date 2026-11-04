// args: OUTFILE hexvalue — instructions with this scalar operand
import ghidra.app.script.GhidraScript;
import ghidra.program.model.listing.*;
import ghidra.program.model.scalar.Scalar;
import java.io.*;
public class ScalarRefs extends GhidraScript {
    public void run() throws Exception {
        String[] a = getScriptArgs();
        PrintWriter out = new PrintWriter(new FileWriter(a[0]));
        long v = Long.parseLong(a[1], 16);
        for (Instruction ins : currentProgram.getListing().getInstructions(true)) {
            for (int i = 0; i < ins.getNumOperands(); i++) {
                Scalar s = ins.getScalar(i);
                if (s != null && s.getUnsignedValue() == v) {
                    Function f = getFunctionContaining(ins.getAddress());
                    out.println(ins.getAddress() + " " + ins + " in " + (f == null ? "?" : f.getName() + "@" + f.getEntryPoint()));
                }
            }
        }
        out.close();
    }
}
