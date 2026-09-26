{attribute 'TcRpcEnable'}
METHOD PUBLIC FillBuffer
VAR_INPUT
    count : UINT;
END_VAR
VAR_OUTPUT
    {attribute 'TcRpcLengthIs' := 'count'}
    values : POINTER TO INT;
END_VAR
VAR
    i : UINT;
END_VAR

IF count > 0 AND values <> 0 THEN
    FOR i := 0 TO count - 1 DO
        values[i] := UINT_TO_INT(i * 2);
    END_FOR
END_IF
