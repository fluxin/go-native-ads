{attribute 'TcRpcEnable'}
METHOD PUBLIC SumBuffer : DINT
VAR_INPUT
    count : UINT;
    {attribute 'TcRpcLengthIs' := 'count'}
    values : POINTER TO INT;
END_VAR
VAR
    i : UINT;
END_VAR

SumBuffer := 0;
IF count > 0 AND values <> 0 THEN
    FOR i := 0 TO count - 1 DO
        SumBuffer := SumBuffer + values[i];
    END_FOR
END_IF
